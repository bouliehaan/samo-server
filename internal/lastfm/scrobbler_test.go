package lastfm

import (
	"fmt"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

var epoch = time.Date(2026, 7, 12, 17, 44, 0, 0, time.UTC)

// listener replays a sequence of requests through the listen engine exactly the
// way the service does, and counts what would be sent upstream.
type listener struct {
	t         *testing.T
	track     catalog.MusicTrack
	play      play
	pointer   nowPlayingPointer
	plays     int
	scrobbles int
	announces []string
	last      TrackSubmission
}

func newListener(t *testing.T, track catalog.MusicTrack) *listener {
	t.Helper()
	return &listener{
		t:     t,
		track: track,
		play:  play{UserID: "user-1", TrackID: track.ID},
	}
}

// submit runs one request through the engine and returns the resulting update.
func (l *listener) submit(input PlaybackInput) playUpdate {
	l.t.Helper()
	input.UserID = "user-1"
	input.Track = l.track
	submission, err := trackSubmission(l.track, input.DurationSeconds)
	if err != nil {
		l.t.Fatalf("trackSubmission: %v", err)
	}
	l.plays++
	update, earned := settle(l.play, observationFrom(input, submission.DurationSeconds), fmt.Sprintf("play-%d", l.plays))
	if earned {
		submission.Timestamp = scrobbleTimestamp(update.Play.StartedAt, input.ObservedAt)
		submission.PlayedSeconds = update.Play.ListenedSeconds
		submission.DedupeKey = scrobbleDedupeKey(submission.TrackID, submission.Artist, submission.Track, submission.Timestamp)
		l.last = submission
		l.scrobbles++
		update.Play.Scrobbled = true
	}
	if shouldAnnounceNowPlaying(update, l.pointer, time.Time{}, input.ObservedAt) {
		l.announces = append(l.announces, update.Play.PlayID)
		l.pointer = nowPlayingPointer{TrackID: update.Play.TrackID, PlayID: update.Play.PlayID, SentAt: input.ObservedAt, Exists: true}
	}
	l.play = update.Play
	return update
}

// stream is `GET /api/v1/music/tracks/{id}/stream`, which reports the position
// playback will resume from.
func (l *listener) stream(at time.Time, resume int) playUpdate {
	return l.submit(PlaybackInput{
		Source:     sourceStream,
		After:      catalog.PlaybackState{ProgressSeconds: resume},
		ObservedAt: at,
	})
}

// progress is the periodic `PATCH /api/v1/playback/music-track/{id}` both
// clients send, on a 20 second timer.
func (l *listener) progress(at time.Time, position int) playUpdate {
	previous := l.play.LastPosition
	return l.submit(PlaybackInput{
		Source:     "playback-patch",
		Before:     catalog.PlaybackState{ProgressSeconds: previous},
		After:      catalog.PlaybackState{ProgressSeconds: position},
		Patch:      &playback.PatchInput{ProgressSeconds: &position},
		ObservedAt: at,
	})
}

// playCount is the end-of-track PATCH that bumps the play counter.
func (l *listener) playCount(at time.Time, position int) playUpdate {
	return l.submit(PlaybackInput{
		Source:     "playback-patch",
		Before:     catalog.PlaybackState{ProgressSeconds: l.play.LastPosition, PlayCount: 2},
		After:      catalog.PlaybackState{ProgressSeconds: position, PlayCount: 3},
		Patch:      &playback.PatchInput{IncrementPlayCount: true, TouchLastPlayedAt: true, ProgressSeconds: &position},
		ObservedAt: at,
	})
}

func (l *listener) skip(at time.Time, position int) playUpdate {
	return l.submit(PlaybackInput{
		Source:     "playback-patch",
		Before:     catalog.PlaybackState{ProgressSeconds: l.play.LastPosition},
		After:      catalog.PlaybackState{ProgressSeconds: position},
		Patch:      &playback.PatchInput{IncrementSkipCount: true, ProgressSeconds: &position},
		ObservedAt: at,
	})
}

// listenThrough plays from `from` to `to` in the client's 20 second steps.
func (l *listener) listenThrough(start time.Time, from, to int) time.Time {
	at := start
	for position := from; position < to; position += 20 {
		at = start.Add(time.Duration(position-from) * time.Second)
		l.progress(at, position)
	}
	at = start.Add(time.Duration(to-from) * time.Second)
	l.progress(at, to)
	return at
}

func trackOf(seconds int) catalog.MusicTrack {
	return catalog.MusicTrack{
		ID:              "track-1",
		Title:           "Signal One",
		ArtistNames:     []string{"The Static"},
		AlbumTitle:      "Night Broadcasts",
		DurationSeconds: seconds,
	}
}

// TestResumeAtEndOfTrackDoesNotScrobble is the defect that dominates the
// production logs. A track played to completion leaves its saved position at
// the end; pressing play again opens the stream at that position, and the old
// code read "position 111 of 111" as a finished listen and scrobbled instantly
// — before a single second had been heard.
func TestResumeAtEndOfTrackDoesNotScrobble(t *testing.T) {
	l := newListener(t, trackOf(111))
	l.stream(epoch, 111)
	if l.scrobbles != 0 {
		t.Fatalf("scrobbles = %d, want 0 (resume position is not a listen)", l.scrobbles)
	}
	if len(l.announces) != 1 {
		t.Fatalf("now playing announcements = %d, want 1", len(l.announces))
	}
}

// TestFullListenScrobblesOnceAtTheThreshold walks the exact request pattern the
// logs show: stream open, then progress every 20 seconds, then the end-of-track
// play-count bump.
func TestFullListenScrobblesOnceAtTheThreshold(t *testing.T) {
	l := newListener(t, trackOf(161)) // threshold: 80 seconds heard

	l.stream(epoch, 0)
	playID := l.play.PlayID
	l.progress(epoch.Add(1*time.Second), 0)
	at := l.listenThrough(epoch.Add(1*time.Second), 0, 60)
	if l.scrobbles != 0 {
		t.Fatalf("scrobbled after %d seconds of listening, want none before the threshold", l.play.ListenedSeconds)
	}

	at = at.Add(20 * time.Second)
	l.progress(at, 80)
	if l.scrobbles != 1 {
		t.Fatalf("scrobbles = %d after crossing the threshold, want 1 (listened=%d)", l.scrobbles, l.play.ListenedSeconds)
	}

	at = l.listenThrough(at, 80, 160)
	l.playCount(at.Add(time.Second), 160)
	if l.scrobbles != 1 {
		t.Fatalf("scrobbles = %d, want 1 (the end-of-track play-count bump re-scrobbled)", l.scrobbles)
	}
	// Now playing may be refreshed while the track runs, but every
	// announcement must be for this one play — never the track that just
	// ended, and never the one queued up next.
	if len(l.announces) == 0 {
		t.Fatal("the track that played was never announced as now playing")
	}
	for _, announced := range l.announces {
		if announced != playID {
			t.Fatalf("announced play %q, want %q", announced, playID)
		}
	}
	// The listen is timestamped when the track started, not when it crossed the
	// threshold, so Last.fm files it in the right place in the history.
	if got := l.last.Timestamp; !got.Equal(epoch) {
		t.Fatalf("scrobble timestamp = %s, want %s (the moment the track started)", got, epoch)
	}
}

// TestPlayingTheSameTrackAgainScrobblesAgain covers the silent-miss half of the
// bug: state used to latch on (user, track) forever, so the second play of any
// track was never scrobbled again.
func TestPlayingTheSameTrackAgainScrobblesAgain(t *testing.T) {
	l := newListener(t, trackOf(120)) // threshold: 60 seconds

	l.stream(epoch, 0)
	end := l.listenThrough(epoch, 0, 120)
	if l.scrobbles != 1 {
		t.Fatalf("first play scrobbles = %d, want 1", l.scrobbles)
	}

	// An hour later the same track is played again. The client opens the stream
	// at the saved position, then reports position 0 as it starts over.
	later := end.Add(time.Hour)
	l.stream(later, 120)
	l.progress(later.Add(time.Second), 0)
	l.listenThrough(later.Add(time.Second), 0, 120)
	if l.scrobbles != 2 {
		t.Fatalf("scrobbles = %d after playing the track twice, want 2", l.scrobbles)
	}
}

// TestRepeatOneScrobblesEveryPass — repeat-one loops back to zero without any
// stream open at all.
func TestRepeatOneScrobblesEveryPass(t *testing.T) {
	l := newListener(t, trackOf(120))
	at := epoch
	for pass := 0; pass < 3; pass++ {
		at = l.listenThrough(at, 0, 120)
		at = at.Add(time.Second)
		l.progress(at, 0) // loops back to the top
	}
	if l.scrobbles != 3 {
		t.Fatalf("scrobbles = %d over three repeat-one passes, want 3", l.scrobbles)
	}
}

// TestMidTrackStreamReopensDoNotResetListening — players reopen the stream
// several times per track for range requests and rebuffering (the logs show
// three in two seconds). Treating each as a new play would reset the measured
// listening and could double-scrobble.
func TestMidTrackStreamReopensDoNotResetListening(t *testing.T) {
	l := newListener(t, trackOf(240)) // threshold: 120 seconds

	l.stream(epoch, 0)
	at := l.listenThrough(epoch, 0, 100)

	// Three reopens in quick succession, reporting the last saved position.
	for i := 0; i < 3; i++ {
		l.stream(at.Add(time.Duration(i)*time.Second), 99)
	}
	if got := l.play.ListenedSeconds; got < 95 {
		t.Fatalf("listened = %d after stream reopens, want the ~100s already heard", got)
	}

	at = l.listenThrough(at.Add(3*time.Second), 100, 240)
	if l.scrobbles != 1 {
		t.Fatalf("scrobbles = %d, want exactly 1 across stream reopens", l.scrobbles)
	}
}

// TestSeekingToTheEndDoesNotScrobble — dragging the scrubber to the end moves a
// lot of audio in no time at all, and is not listening.
func TestSeekingToTheEndDoesNotScrobble(t *testing.T) {
	l := newListener(t, trackOf(300)) // threshold: 150 seconds

	l.stream(epoch, 0)
	l.progress(epoch.Add(5*time.Second), 5)
	l.progress(epoch.Add(6*time.Second), 295) // scrubbed to the end
	l.playCount(epoch.Add(7*time.Second), 300)

	if l.scrobbles != 0 {
		t.Fatalf("scrobbles = %d, want 0 (scrubbing to the end is not a listen)", l.scrobbles)
	}
}

// TestPauseCreditsNothing — a paused client keeps reporting the same position.
func TestPauseCreditsNothing(t *testing.T) {
	l := newListener(t, trackOf(300))
	l.stream(epoch, 0)
	at := l.listenThrough(epoch, 0, 60)
	for i := 1; i <= 10; i++ {
		l.progress(at.Add(time.Duration(i)*20*time.Second), 60)
	}
	if got := l.play.ListenedSeconds; got > 65 {
		t.Fatalf("listened = %d after ten minutes paused at 60s, want ~60", got)
	}
	if l.scrobbles != 0 {
		t.Fatalf("scrobbles = %d, want 0", l.scrobbles)
	}
}

// TestGarbagePositionIsIgnored — clients have reported milliseconds as seconds
// (position 20853670 on a 257 second track). The old code scrobbled it with a
// timestamp eight months in the past.
func TestGarbagePositionIsIgnored(t *testing.T) {
	l := newListener(t, trackOf(257))
	l.stream(epoch, 20853670)
	if l.scrobbles != 0 {
		t.Fatalf("scrobbles = %d, want 0 for an impossible position", l.scrobbles)
	}
	if l.play.LastPosition != 0 {
		t.Fatalf("last position = %d, want 0 (the garbage value was stored)", l.play.LastPosition)
	}
	l.progress(epoch.Add(20*time.Second), 65440590)
	if l.scrobbles != 0 || l.play.ListenedSeconds != 0 {
		t.Fatalf("garbage progress credited %d seconds and %d scrobbles", l.play.ListenedSeconds, l.scrobbles)
	}
}

// TestTracksShorterThanThirtySecondsAreNeverScrobbled is a Last.fm rule.
func TestTracksShorterThanThirtySecondsAreNeverScrobbled(t *testing.T) {
	l := newListener(t, trackOf(20))
	l.stream(epoch, 0)
	l.listenThrough(epoch, 0, 20)
	l.playCount(epoch.Add(21*time.Second), 20)
	if l.scrobbles != 0 {
		t.Fatalf("scrobbles = %d, want 0 for a 20 second track", l.scrobbles)
	}
}

// TestShortTrackNeedsThirtySecondsHeard — for a 40 second track half the
// duration is under the 30 second floor, so the floor governs.
func TestShortTrackNeedsThirtySecondsHeard(t *testing.T) {
	l := newListener(t, trackOf(40))
	l.stream(epoch, 0)
	l.progress(epoch.Add(20*time.Second), 20)
	if l.scrobbles != 0 {
		t.Fatalf("scrobbled after 20 seconds of a 40 second track")
	}
	l.progress(epoch.Add(35*time.Second), 35)
	if l.scrobbles != 1 {
		t.Fatalf("scrobbles = %d after 35 seconds heard, want 1", l.scrobbles)
	}
}

// TestLongTrackScrobblesAtFourMinutes — the threshold caps at four minutes.
func TestLongTrackScrobblesAtFourMinutes(t *testing.T) {
	l := newListener(t, trackOf(900))
	l.stream(epoch, 0)
	at := l.listenThrough(epoch, 0, 220)
	if l.scrobbles != 0 {
		t.Fatalf("scrobbled after %d seconds of a 15 minute track", l.play.ListenedSeconds)
	}
	l.listenThrough(at, 220, 245)
	if l.scrobbles != 1 {
		t.Fatalf("scrobbles = %d after four minutes heard, want 1", l.scrobbles)
	}
}

// TestEndOfTrackWithPositionResetStillCounts — some clients zero the position
// as they advance the queue, so the final PATCH says "play count +1, position
// 0". The last unreported stretch still has to be credited.
func TestEndOfTrackWithPositionResetStillCounts(t *testing.T) {
	l := newListener(t, trackOf(200)) // threshold: 100 seconds

	l.stream(epoch, 0)
	at := l.listenThrough(epoch, 0, 80)
	if l.scrobbles != 0 {
		t.Fatalf("scrobbled early")
	}
	// 40 seconds of silence from the client, then "the track finished".
	l.playCount(at.Add(40*time.Second), 0)
	if l.scrobbles != 1 {
		t.Fatalf("scrobbles = %d, want 1 (listened=%d)", l.scrobbles, l.play.ListenedSeconds)
	}
}

// TestSkipBeforeThresholdDoesNotScrobble, and closes the play so the next one
// starts clean.
func TestSkipBeforeThresholdDoesNotScrobble(t *testing.T) {
	l := newListener(t, trackOf(200))
	l.stream(epoch, 0)
	at := l.listenThrough(epoch, 0, 40)
	l.skip(at.Add(time.Second), 41)
	if l.scrobbles != 0 {
		t.Fatalf("scrobbles = %d, want 0", l.scrobbles)
	}
	if !l.play.Closed {
		t.Fatal("a skipped play must be closed")
	}
}

// TestSkipAfterThresholdKeepsTheScrobble — Last.fm counts a listen the moment
// the threshold is met; skipping the tail does not retract it.
func TestSkipAfterThresholdKeepsTheScrobble(t *testing.T) {
	l := newListener(t, trackOf(200))
	l.stream(epoch, 0)
	at := l.listenThrough(epoch, 0, 120)
	if l.scrobbles != 1 {
		t.Fatalf("scrobbles = %d before the skip, want 1", l.scrobbles)
	}
	l.skip(at.Add(time.Second), 121)
	if l.scrobbles != 1 {
		t.Fatalf("scrobbles = %d after the skip, want 1", l.scrobbles)
	}
}

// TestOutOfOrderObservationDoesNotRewind — every notification runs on its own
// goroutine, so a progress report can overtake an earlier one.
func TestOutOfOrderObservationDoesNotRewind(t *testing.T) {
	l := newListener(t, trackOf(300))
	l.stream(epoch, 0)
	at := l.listenThrough(epoch, 0, 100)
	listened := l.play.ListenedSeconds

	// A report stamped 40 seconds ago arrives now.
	l.submit(PlaybackInput{
		Source:     "playback-patch",
		After:      catalog.PlaybackState{ProgressSeconds: 60},
		Patch:      &playback.PatchInput{ProgressSeconds: intPtr(60)},
		ObservedAt: at.Add(-40 * time.Second),
	})
	if l.play.ListenedSeconds != listened {
		t.Fatalf("listened = %d after a stale report, want %d unchanged", l.play.ListenedSeconds, listened)
	}
	if l.play.LastPosition != 100 {
		t.Fatalf("last position = %d after a stale report, want 100", l.play.LastPosition)
	}
}

// TestIdlePlayIsNotResumedHours later — an abandoned play must not absorb a
// listen that happens the next day and scrobble it with yesterday's timestamp.
func TestIdlePlayIsNotResumedHoursLater(t *testing.T) {
	l := newListener(t, trackOf(300))
	l.stream(epoch, 0)
	at := l.listenThrough(epoch, 0, 100)

	tomorrow := at.Add(20 * time.Hour)
	update := l.submit(PlaybackInput{
		Source:     "playback-patch",
		After:      catalog.PlaybackState{ProgressSeconds: 120},
		Patch:      &playback.PatchInput{ProgressSeconds: intPtr(120)},
		ObservedAt: tomorrow,
	})
	if !update.Started {
		t.Fatal("a report 20 hours later must begin a new play")
	}
	if l.play.ListenedSeconds != 0 {
		t.Fatalf("listened = %d, want 0 for a fresh play", l.play.ListenedSeconds)
	}
}

// TestExplicitCompleteEventIsTrusted — POST /scrobble/events documents
// `complete` as "this track finished", so a client that reports nothing else
// still gets its listen recorded.
func TestExplicitCompleteEventIsTrusted(t *testing.T) {
	l := newListener(t, trackOf(240))
	l.submit(PlaybackInput{Source: "scrobble-event", Event: EventStart, ObservedAt: epoch})
	l.submit(PlaybackInput{
		Source:     "scrobble-event",
		Event:      EventComplete,
		After:      catalog.PlaybackState{ProgressSeconds: 240},
		ObservedAt: epoch.Add(240 * time.Second),
	})
	if l.scrobbles != 1 {
		t.Fatalf("scrobbles = %d, want 1 for an explicit complete event", l.scrobbles)
	}
}

// TestExplicitStartBeginsAFreshPlay.
func TestExplicitStartBeginsAFreshPlay(t *testing.T) {
	l := newListener(t, trackOf(240))
	l.stream(epoch, 0)
	at := l.listenThrough(epoch, 0, 100)
	update := l.submit(PlaybackInput{Source: "scrobble-event", Event: EventStart, ObservedAt: at.Add(time.Second)})
	if !update.Started || l.play.ListenedSeconds != 0 {
		t.Fatalf("explicit start must reset the play (started=%v listened=%d)", update.Started, l.play.ListenedSeconds)
	}
}

// TestNowPlayingSuppressedForPrefetchedTrack — a gapless client opens the next
// track's stream about a minute before it actually starts. Announcing it then
// puts the wrong song on the listener's profile.
func TestNowPlayingSuppressedForPrefetchedTrack(t *testing.T) {
	next := newListener(t, catalog.MusicTrack{
		ID: "track-2", Title: "Second", ArtistNames: []string{"The Static"}, DurationSeconds: 200,
	})
	update, _ := settle(next.play, observationFrom(PlaybackInput{
		UserID: "user-1", Track: next.track, Source: sourceStream,
		After: catalog.PlaybackState{ProgressSeconds: 0}, ObservedAt: epoch,
	}, 200), "play-1")

	// The track actually playing advanced three seconds ago.
	if shouldAnnounceNowPlaying(update, nowPlayingPointer{}, epoch.Add(-3*time.Second), epoch) {
		t.Fatal("a prefetched track must not be announced while another is still advancing")
	}
	// Once the previous track has been quiet long enough, it is genuinely next.
	if !shouldAnnounceNowPlaying(update, nowPlayingPointer{}, epoch.Add(-2*time.Minute), epoch) {
		t.Fatal("the track that took over must be announced")
	}
}

// TestNowPlayingRefreshesWhileStillPlaying — Last.fm expires "now playing" on
// its own, so a long track must keep it alive.
func TestNowPlayingRefreshesWhileStillPlaying(t *testing.T) {
	l := newListener(t, trackOf(900))
	l.stream(epoch, 0)
	playID := l.play.PlayID
	if len(l.announces) != 1 {
		t.Fatalf("announcements = %d, want 1 at the start", len(l.announces))
	}
	l.listenThrough(epoch, 0, 600)
	if len(l.announces) < 3 {
		t.Fatalf("announcements = %d over ten minutes, want periodic refreshes", len(l.announces))
	}
	for _, announced := range l.announces {
		if announced != playID {
			t.Fatalf("announced play %q, want %q", announced, playID)
		}
	}
}

// TestFavouritingDoesNotAnnounceNowPlaying — a favourite arrives as a playback
// PATCH, which must not put the track on the listener's profile.
func TestFavouritingDoesNotAnnounceNowPlaying(t *testing.T) {
	l := newListener(t, trackOf(200))
	favorite := true
	l.submit(PlaybackInput{
		Source:     "playback-patch",
		After:      catalog.PlaybackState{Favorite: true, ProgressSeconds: 90},
		Patch:      &playback.PatchInput{Favorite: &favorite},
		ObservedAt: epoch,
	})
	if len(l.announces) != 0 {
		t.Fatalf("announcements = %d, want 0 for a favourite", len(l.announces))
	}
	if l.scrobbles != 0 {
		t.Fatalf("scrobbles = %d, want 0 for a favourite", l.scrobbles)
	}
}

func TestCreditForBoundsListeningByBothContentAndTime(t *testing.T) {
	tests := []struct {
		name       string
		contentGap int
		wallGap    int
		want       int
	}{
		{name: "steady playback", contentGap: 20, wallGap: 20, want: 20},
		{name: "clock jitter is tolerated", contentGap: 20, wallGap: 17, want: 20},
		{name: "seek forward credits only elapsed time", contentGap: 200, wallGap: 1, want: 6},
		{name: "paused", contentGap: 0, wallGap: 60, want: 0},
		{name: "rewound", contentGap: -30, wallGap: 5, want: 0},
		{name: "no time passed", contentGap: 30, wallGap: 0, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := creditFor(tc.contentGap, tc.wallGap); got != tc.want {
				t.Fatalf("creditFor(%d, %d) = %d, want %d", tc.contentGap, tc.wallGap, got, tc.want)
			}
		})
	}
}

func TestScrobbleThreshold(t *testing.T) {
	tests := []struct{ duration, want int }{
		{duration: 0, want: 240},
		{duration: 120, want: 60},
		{duration: 480, want: 240},
		{duration: 900, want: 240},
	}
	for _, tc := range tests {
		if got := scrobbleThreshold(tc.duration); got != tc.want {
			t.Fatalf("scrobbleThreshold(%d) = %d, want %d", tc.duration, got, tc.want)
		}
	}
}

func TestSanitizePosition(t *testing.T) {
	tests := []struct {
		name     string
		position int
		duration int
		want     int
		ok       bool
	}{
		{name: "normal", position: 100, duration: 200, want: 100, ok: true},
		{name: "slight overshoot is clamped", position: 205, duration: 200, want: 200, ok: true},
		{name: "milliseconds masquerading as seconds", position: 20853670, duration: 257, ok: false},
		{name: "negative", position: -5, duration: 200, ok: false},
		{name: "unknown duration allows a long track", position: 3600, duration: 0, want: 3600, ok: true},
		{name: "unknown duration rejects nonsense", position: 90000, duration: 0, ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := sanitizePosition(tc.position, tc.duration)
			if ok != tc.ok {
				t.Fatalf("sanitizePosition(%d, %d) ok = %v, want %v", tc.position, tc.duration, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("sanitizePosition(%d, %d) = %d, want %d", tc.position, tc.duration, got, tc.want)
			}
		})
	}
}

func TestScrobbleTimestampStaysInLastFMsWindow(t *testing.T) {
	now := epoch
	if got := scrobbleTimestamp(now.Add(time.Hour), now); !got.Equal(now) {
		t.Fatalf("a future start must be clamped to now, got %s", got)
	}
	if got := scrobbleTimestamp(now.Add(-90*24*time.Hour), now); now.Sub(got) > maxScrobbleAge {
		t.Fatalf("a very old start must be clamped into the accepted window, got %s", got)
	}
	started := now.Add(-3 * time.Minute)
	if got := scrobbleTimestamp(started, now); !got.Equal(started) {
		t.Fatalf("an ordinary start must be preserved, got %s", got)
	}
}

func TestScrobbleDedupeKeyIdentifiesTheListen(t *testing.T) {
	at := epoch
	base := scrobbleDedupeKey("track-1", "The Static", "Signal One", at)
	if base != scrobbleDedupeKey("track-1", "the static", "SIGNAL ONE", at) {
		t.Fatal("the key must not depend on metadata casing")
	}
	if base == scrobbleDedupeKey("track-1", "The Static", "Signal One", at.Add(time.Second)) {
		t.Fatal("two plays a second apart must produce different keys")
	}
	if base == scrobbleDedupeKey("track-2", "The Static", "Signal One", at) {
		t.Fatal("different tracks must produce different keys")
	}
}

func TestRetryDelayBacksOffAndCaps(t *testing.T) {
	var previous time.Duration
	for attempt := 1; attempt <= 12; attempt++ {
		delay := retryDelay(attempt)
		if delay < queueBaseDelay {
			t.Fatalf("retryDelay(%d) = %s, want at least %s", attempt, delay, queueBaseDelay)
		}
		if delay > queueMaxDelay+queueMaxDelay/4 {
			t.Fatalf("retryDelay(%d) = %s, want at most %s", attempt, delay, queueMaxDelay)
		}
		if attempt > 1 && attempt < 9 && delay <= previous {
			t.Fatalf("retryDelay(%d) = %s did not grow past %s", attempt, delay, previous)
		}
		previous = delay
	}
}
