package lastfm

// The listen engine.
//
// Everything in this file is pure: given the stored state of a play and one
// observation of it, produce the next state and what should be sent upstream.
// No database, no HTTP, no clock — which is why it can be tested exhaustively
// against the exact request sequences real clients produce.
//
// The governing idea is that a scrobble must be earned by AUDIO THAT ADVANCED,
// never by a position a client happened to report. A client that says "I'm at
// 247 of 248 seconds" has told us nothing about whether anyone listened; it may
// simply have resumed where the last play left off. Only the difference between
// consecutive positions, bounded by the wall-clock time between them, is real
// listening. That single rule removes every phantom scrobble in the production
// logs and, because listening accumulates per play rather than latching per
// track, it also removes the silent misses on the second play of a track.

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

const (
	// Last.fm's published listen rules.
	minScrobbleTrackSeconds = 30  // tracks shorter than this are never scrobbled
	minListenSeconds        = 30  // floor on time actually heard
	maxScrobbleThreshold    = 240 // ...or half the track, whichever is lower

	// A position at or below this, after having played well past it, is a
	// restart (repeat-one, seek-to-top, or the next play of the same track)
	// rather than a rewind within the current play.
	restartPositionSeconds = 5

	// Wall-clock jitter tolerated when crediting listening. Clients report on a
	// timer (20s in practice), and network delay makes the measured interval
	// drift a second or two either way; without slack that drift compounds into
	// a systematic under-count that can strand a track just below threshold.
	// It stays small enough that a seek — tens of seconds of content in ~zero
	// wall time — still credits almost nothing.
	creditSlackSeconds = 5

	// How close to the end counts as "the track finished".
	endOfTrackGraceSeconds = 2

	// A play with no observations for this long is over; the next report starts
	// a fresh one. Long enough to survive a paused track, short enough that
	// yesterday's play never absorbs today's.
	playIdleTimeout = time.Hour

	// Position sanity. Clients have been seen reporting milliseconds (a 257s
	// track at "position" 20853670), which the old code scrobbled with a
	// timestamp eight months in the past.
	maxPositionSeconds     = 24 * 3600
	positionOvershootGrace = 30

	// Last.fm expires "now playing" on its own; refresh while audio is still
	// advancing so a long track does not go blank mid-play.
	nowPlayingRefresh = 150 * time.Second

	// Gapless clients open the NEXT track's stream while the current one is
	// still playing. Suppress that announcement while another track is
	// demonstrably still advancing.
	nowPlayingPrefetchGuard = 45 * time.Second

	// Last.fm rejects scrobbles older than two weeks. maxScrobbleAge is the
	// point past which delivery is abandoned; scrobbleClampAge is where an
	// older timestamp is pinned, deliberately inside it so a clamped listen
	// does not land exactly on the drop boundary and get discarded on its way
	// out.
	maxScrobbleAge   = 13 * 24 * time.Hour
	scrobbleClampAge = maxScrobbleAge - 12*time.Hour
)

// observationKind is what a request tells us about the play, beyond position.
type observationKind int

const (
	obsProgress observationKind = iota // routine position report
	obsStart                           // client explicitly declared a new play
	obsFinish                          // the track reached its end
	obsSkip                            // the listener abandoned the track
)

// observation is one report about one track at one instant.
type observation struct {
	Kind        observationKind
	Position    int
	HasPosition bool
	Duration    int
	At          time.Time
	// Trusted marks an explicit client assertion from POST /scrobble/events,
	// whose documented contract is "complete means this track finished". Only
	// that endpoint may credit listening the server did not measure itself;
	// an incidental play-count bump on a playback PATCH may not.
	Trusted bool
	// Begins marks the two signals that unambiguously mean playback is
	// starting — opening the stream, or an explicit `start` event. Only these
	// may announce "now playing" before any audio has been seen to advance, so
	// that e.g. favouriting a track never announces it.
	Begins bool
}

// play is the durable state of one listen of one track.
type play struct {
	UserID          string
	TrackID         string
	PlayID          string
	StartedAt       time.Time
	LastPosition    int
	LastObservedAt  time.Time
	LastAdvanceAt   time.Time
	ListenedSeconds int
	DurationSeconds int
	Scrobbled       bool
	Closed          bool
	Exists          bool
}

// playUpdate is the result of folding one observation into a play.
type playUpdate struct {
	Play     play
	Started  bool // a new play began
	Advanced bool // real listening was credited
	Finished bool // the play ended
	Stale    bool // the observation arrived out of order and was ignored
	Begins   bool // the observation was an explicit start of playback
}

// settle folds one observation into a play and reports whether the result has
// earned a scrobble. Everything the service adds around this is durability.
func settle(current play, obs observation, newPlayID string) (playUpdate, bool) {
	update := applyObservation(current, obs, newPlayID)
	if update.Finished {
		update.Play.Closed = true
	}
	return update, qualifiesForScrobble(update.Play)
}

// applyObservation folds obs into current and returns the next state.
func applyObservation(current play, obs observation, newPlayID string) playUpdate {
	if !current.Exists || current.Closed || obs.Kind == obsStart ||
		isRestart(current, obs) || isIdle(current, obs) ||
		(obs.Begins && atEndOfTrack(current)) {
		return finalize(playUpdate{Play: beginPlay(current, obs, newPlayID), Started: true}, current, obs)
	}
	return finalize(playUpdate{Play: current}, current, obs)
}

// atEndOfTrack reports whether a play is sitting at the end of its track. Such
// a play cannot be continued: pressing play on it is a new listen, not a
// resumption, and continuing it would leave the listener with no "now playing"
// at all until their client's next position report.
func atEndOfTrack(p play) bool {
	return p.Exists && p.DurationSeconds > 0 &&
		p.LastPosition >= p.DurationSeconds-endOfTrackGraceSeconds
}

// beginPlay starts a fresh play at the observed position. Listening starts at
// zero no matter where the position is: resuming at 4:07 of a 4:08 track means
// nothing has been heard yet.
func beginPlay(current play, obs observation, playID string) play {
	position := 0
	if obs.HasPosition {
		position = obs.Position
	}
	duration := current.DurationSeconds
	if obs.Duration > 0 {
		duration = obs.Duration
	}
	return play{
		UserID:          current.UserID,
		TrackID:         current.TrackID,
		PlayID:          playID,
		StartedAt:       obs.At.Add(-time.Duration(position) * time.Second),
		LastPosition:    position,
		LastObservedAt:  obs.At,
		DurationSeconds: duration,
		Exists:          true,
	}
}

// finalize credits listening for one observation and settles the play's end
// state. `previous` supplies the position and instant the observation is
// measured against; for a play that just began they are the play's own, so
// nothing is credited.
func finalize(update playUpdate, previous play, obs observation) playUpdate {
	next := update.Play
	update.Begins = obs.Begins
	if obs.Duration > 0 {
		next.DurationSeconds = obs.Duration
	}
	if update.Started {
		previous = next
	}

	// An observation timestamped before the newest one we have already folded
	// in overtook it in flight (every notify path runs in its own goroutine).
	// Honour its terminal meaning, but never rewind measured state with it.
	update.Stale = obs.At.Before(previous.LastObservedAt)

	position := previous.LastPosition
	if obs.HasPosition && !update.Stale {
		position = obs.Position
	}
	// The end of a track is frequently reported as position 0 (clients reset
	// the counter as they advance the queue) or simply stops being reported at
	// all. Treat a finish as an observation AT the end so the last unreported
	// stretch is credited — still bounded by wall clock, so skipping to the end
	// credits nothing.
	if obs.Kind == obsFinish && !obs.Trusted && !update.Stale &&
		next.DurationSeconds > 0 && position < next.DurationSeconds {
		position = next.DurationSeconds
	}

	if !update.Stale {
		wallGap := int(obs.At.Sub(previous.LastObservedAt) / time.Second)
		if credit := creditFor(position-previous.LastPosition, wallGap); credit > 0 {
			next.ListenedSeconds += credit
			next.LastAdvanceAt = obs.At
			update.Advanced = true
		}
		next.LastPosition = position
		next.LastObservedAt = obs.At
	}

	// An explicit `complete` event is the client asserting the track played
	// through; honour its own account of how much was heard.
	if obs.Kind == obsFinish && obs.Trusted {
		claimed := obs.Position
		if next.DurationSeconds > 0 && (claimed <= 0 || claimed > next.DurationSeconds) {
			claimed = next.DurationSeconds
		}
		if claimed > next.ListenedSeconds {
			next.ListenedSeconds = claimed
		}
	}
	if next.DurationSeconds > 0 && next.ListenedSeconds > next.DurationSeconds {
		next.ListenedSeconds = next.DurationSeconds
	}

	switch {
	case obs.Kind == obsFinish, obs.Kind == obsSkip:
		update.Finished = true
	case !update.Started && next.DurationSeconds > 0 &&
		next.LastPosition >= next.DurationSeconds-endOfTrackGraceSeconds:
		// Reaching the end closes the play so the NEXT report of this track
		// starts a new one. Without this, replaying a track whose first report
		// lands mid-track (a client on a 20s timer easily misses position 0)
		// would inherit the finished play's "already scrobbled" and be lost.
		//
		// A play that BEGINS at the end has not finished, it has resumed there:
		// pressing play on a track left at 4:07 of 4:08 must still count as
		// playing, or nothing is announced as now playing.
		update.Finished = true
	}
	update.Play = next
	return update
}

// creditFor converts one observation interval into listening time: at most the
// audio that advanced, and at most the real time that elapsed. A seek forward
// moves a lot of content in no time and so credits nothing; a pause moves time
// but no content and likewise credits nothing.
func creditFor(contentGap, wallGap int) int {
	if contentGap <= 0 || wallGap <= 0 {
		// No time passed, so nothing was heard. The jitter allowance below is
		// deliberately withheld here: a client flushing a backlog of buffered
		// position updates on reconnect fires many reports in the same instant,
		// and a per-report allowance would add up to a listen that never
		// happened.
		return 0
	}
	if limit := wallGap + creditSlackSeconds; contentGap > limit {
		return limit
	}
	return contentGap
}

// isRestart reports whether the position jumped back to the very start of a
// track that had played well past it — repeat-one, seek-to-top, or simply
// playing it again.
func isRestart(current play, obs observation) bool {
	if !obs.HasPosition || obs.Kind == obsFinish || obs.Kind == obsSkip {
		return false
	}
	return current.LastPosition > restartPositionSeconds && obs.Position <= restartPositionSeconds
}

func isIdle(current play, obs observation) bool {
	if current.LastObservedAt.IsZero() {
		return false
	}
	return obs.At.Sub(current.LastObservedAt) > playIdleTimeout
}

// qualifiesForScrobble applies Last.fm's listen rules to MEASURED listening.
func qualifiesForScrobble(p play) bool {
	if p.Scrobbled {
		return false
	}
	if p.DurationSeconds > 0 && p.DurationSeconds < minScrobbleTrackSeconds {
		return false
	}
	if p.ListenedSeconds < minListenSeconds {
		return false
	}
	return p.ListenedSeconds >= scrobbleThreshold(p.DurationSeconds)
}

func scrobbleThreshold(durationSeconds int) int {
	if durationSeconds <= 0 {
		return maxScrobbleThreshold
	}
	if half := durationSeconds / 2; half < maxScrobbleThreshold {
		return half
	}
	return maxScrobbleThreshold
}

// nowPlayingPointer is the track a user was last announced — or last attempted
// to be announced — as playing. A zero SentAt records an attempt that failed:
// the pointer still moves, so the failure is audited once rather than on every
// position report, while the refresh throttle stays disengaged so the next
// report retries immediately.
type nowPlayingPointer struct {
	TrackID string
	PlayID  string
	SentAt  time.Time
	Exists  bool
}

// announcedFor reports whether Last.fm currently shows this play.
func (p nowPlayingPointer) announcedFor(current play) bool {
	return p.Exists && p.TrackID == current.TrackID && p.PlayID == current.PlayID && !p.SentAt.IsZero()
}

// shouldAnnounceNowPlaying decides whether this observation should update
// Last.fm's "now playing". otherAdvancedAt is the last time a DIFFERENT track
// of the same user credited real listening.
func shouldAnnounceNowPlaying(update playUpdate, pointer nowPlayingPointer, otherAdvancedAt time.Time, at time.Time) bool {
	p := update.Play
	if update.Stale || p.Closed {
		return false
	}
	announced := pointer.announcedFor(p)
	if announced && at.Sub(pointer.SentAt) < nowPlayingRefresh {
		return false
	}
	if update.Advanced {
		// Audio is moving: this is unambiguously what the user is hearing.
		return true
	}
	if update.Started && update.Begins && !announced {
		// Playback has been declared but no audio has moved yet. Announce it
		// unless another track is still audibly running, which means this one
		// was merely prefetched by a gapless player.
		return otherAdvancedAt.IsZero() || at.Sub(otherAdvancedAt) >= nowPlayingPrefetchGuard
	}
	return false
}

// scrobbleTimestamp is the instant Last.fm records the play at: when the track
// started, clamped into the window Last.fm accepts.
func scrobbleTimestamp(startedAt, now time.Time) time.Time {
	if startedAt.IsZero() || startedAt.After(now) {
		return now
	}
	if now.Sub(startedAt) > scrobbleClampAge {
		return now.Add(-scrobbleClampAge)
	}
	return startedAt
}

// scrobbleDedupeKey identifies a scrobble by what it claims, not by which code
// path produced it, so the same play can never be submitted twice however it
// is rediscovered. Track plus start-second is unique in practice: two distinct
// plays of one track cannot begin in the same second.
func scrobbleDedupeKey(trackID, artist, track string, timestamp time.Time) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(trackID),
		strings.ToLower(strings.TrimSpace(artist)),
		strings.ToLower(strings.TrimSpace(track)),
		strconv.FormatInt(timestamp.Unix(), 10),
	}, "\x1f")))
	return hex.EncodeToString(sum[:16])
}

// sanitizePosition rejects positions that cannot describe this track. Returning
// ok=false drops only the POSITION: an observation that also carries a finish or
// skip still counts, it just cannot move or credit the play.
func sanitizePosition(seconds, durationSeconds int) (int, bool) {
	if seconds < 0 {
		return 0, false
	}
	ceiling := maxPositionSeconds
	if durationSeconds > 0 {
		ceiling = durationSeconds + positionOvershootGrace
	}
	if seconds > ceiling {
		return 0, false
	}
	if durationSeconds > 0 && seconds > durationSeconds {
		seconds = durationSeconds
	}
	return seconds, true
}

// observationFrom translates a request into an observation.
func observationFrom(input PlaybackInput, durationSeconds int) observation {
	obs := observation{
		Duration: durationSeconds,
		At:       input.ObservedAt,
		Trusted:  input.Event == EventComplete,
		Begins:   input.Source == sourceStream || input.Event == EventStart,
		Kind:     observationKindFrom(input),
	}
	if obs.At.IsZero() {
		obs.At = time.Now().UTC()
	}
	if raw, reported := reportedPosition(input); reported {
		if position, ok := sanitizePosition(raw, durationSeconds); ok {
			obs.Position, obs.HasPosition = position, true
		}
	}
	return obs
}

func observationKindFrom(input PlaybackInput) observationKind {
	switch input.Event {
	case EventStart:
		return obsStart
	case EventSkip:
		return obsSkip
	case EventComplete:
		return obsFinish
	case EventProgress:
		return obsProgress
	}
	if skipped(input) {
		return obsSkip
	}
	if completed(input) {
		return obsFinish
	}
	// A stream open is deliberately NOT a start: players reopen the same stream
	// several times per track for range requests and rebuffering, and treating
	// each as a new play would reset measured listening until nothing ever
	// reached the threshold. It is just a position report; if it lands back at
	// the top of a finished track, isRestart picks that up.
	return obsProgress
}

func skipped(input PlaybackInput) bool {
	if input.Patch == nil {
		return false
	}
	if input.Patch.IncrementSkipCount {
		return true
	}
	return input.Patch.SkipCount != nil && *input.Patch.SkipCount > input.Before.SkipCount
}

func completed(input PlaybackInput) bool {
	if input.After.Completed {
		return true
	}
	if input.Patch == nil {
		return false
	}
	if input.Patch.Completed != nil && *input.Patch.Completed {
		return true
	}
	// Both clients bump the play count at the natural end of a track.
	if input.Patch.IncrementPlayCount {
		return true
	}
	return input.Patch.PlayCount != nil && *input.Patch.PlayCount > input.Before.PlayCount
}

func reportedPosition(input PlaybackInput) (int, bool) {
	if input.Patch != nil && input.Patch.ProgressSeconds != nil {
		return *input.Patch.ProgressSeconds, true
	}
	if input.Event != "" || input.Source == sourceStream {
		return input.After.ProgressSeconds, true
	}
	if input.After.ProgressSeconds > 0 || input.Before.ProgressSeconds > 0 {
		return input.After.ProgressSeconds, true
	}
	return 0, false
}

// trackSubmission snapshots the metadata Last.fm needs for a track.
func trackSubmission(track catalog.MusicTrack, durationOverride int) (TrackSubmission, error) {
	artist := strings.TrimSpace(track.DisplayArtist)
	if artist == "" && len(track.ArtistNames) > 0 {
		artist = strings.Join(track.ArtistNames, ", ")
	}
	if artist == "" && len(track.AlbumArtistNames) > 0 {
		artist = strings.Join(track.AlbumArtistNames, ", ")
	}
	title := strings.TrimSpace(track.Title)
	if artist == "" || title == "" {
		return TrackSubmission{}, ErrMissingMetadata
	}
	duration := durationOverride
	if duration <= 0 {
		duration = track.DurationSeconds
	}
	if duration <= 0 && len(track.AudioFiles) > 0 {
		duration = track.AudioFiles[0].DurationSeconds
	}
	if duration < 0 {
		duration = 0
	}
	return TrackSubmission{
		TrackID:              track.ID,
		Artist:               artist,
		Track:                title,
		Album:                strings.TrimSpace(track.AlbumTitle),
		DurationSeconds:      duration,
		MusicBrainzRecording: strings.TrimSpace(track.ExternalIDs.MusicBrainzRecordingID),
	}, nil
}

func loveStateChanged(before, after catalog.PlaybackState, patch *playback.PatchInput) (loved bool, unloved bool) {
	beforeLoved := before.Favorite || before.Starred
	afterLoved := after.Favorite || after.Starred
	if patch != nil {
		if patch.Favorite != nil {
			afterLoved = *patch.Favorite || after.Starred
		}
		if patch.Starred != nil {
			afterLoved = after.Favorite || *patch.Starred
		}
	}
	if !beforeLoved && afterLoved {
		return true, false
	}
	if beforeLoved && !afterLoved {
		return false, true
	}
	return false, false
}

func parseScrobbleEvent(raw string) (ScrobbleEvent, error) {
	switch ScrobbleEvent(strings.ToLower(strings.TrimSpace(raw))) {
	case EventStart:
		return EventStart, nil
	case EventProgress:
		return EventProgress, nil
	case EventComplete:
		return EventComplete, nil
	case EventSkip:
		return EventSkip, nil
	default:
		return "", ErrInvalidEvent
	}
}
