package lastfm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// clock drives the listen engine and the retry schedule from the test.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: epoch} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeLastFM records every signed call and lets a test choose the reply.
type fakeLastFM struct {
	mu     sync.Mutex
	server *httptest.Server
	calls  []url.Values
	reply  func(method string, form url.Values) (int, string)
}

func newFakeLastFM(t *testing.T) *fakeLastFM {
	t.Helper()
	api := &fakeLastFM{}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		form := r.PostForm
		api.mu.Lock()
		api.calls = append(api.calls, form)
		reply := api.reply
		api.mu.Unlock()

		status, body := http.StatusOK, `{}`
		if form.Get("method") == "track.scrobble" {
			body = `{"scrobbles":{"@attr":{"accepted":1,"ignored":0},"scrobble":{}}}`
		}
		if reply != nil {
			if s, b := reply(form.Get("method"), form); s != 0 {
				status, body = s, b
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(api.server.Close)
	return api
}

func (a *fakeLastFM) setReply(fn func(method string, form url.Values) (int, string)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reply = fn
}

func (a *fakeLastFM) of(method string) []url.Values {
	a.mu.Lock()
	defer a.mu.Unlock()
	matched := make([]url.Values, 0, len(a.calls))
	for _, call := range a.calls {
		if call.Get("method") == method {
			matched = append(matched, call)
		}
	}
	return matched
}

func (a *fakeLastFM) count(method string) int { return len(a.of(method)) }

func (a *fakeLastFM) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = nil
}

type harness struct {
	t       *testing.T
	db      *sql.DB
	api     *fakeLastFM
	clock   *clock
	service *Service
	track   catalog.MusicTrack
	plays   int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := openTestDB(t)
	seedLastFMSession(t, db)
	api := newFakeLastFM(t)
	c := newClock()
	h := &harness{t: t, db: db, api: api, clock: c, track: trackOf(161)}
	h.service = newTestService(t, db, api.server, func(options *ServiceOptions) {
		options.Now = c.Now
		options.NewPlayID = func() string {
			h.plays++
			return fmt.Sprintf("play-%d", h.plays)
		}
	})
	return h
}

// stream replays `GET /music/tracks/{id}/stream`.
func (h *harness) stream(resume int) {
	h.t.Helper()
	h.service.HandlePlayback(context.Background(), PlaybackInput{
		UserID:     users.BootstrapUserID,
		Track:      h.track,
		Source:     sourceStream,
		After:      catalog.PlaybackState{ProgressSeconds: resume},
		ObservedAt: h.clock.Now(),
	})
}

// progress replays the periodic playback PATCH, advancing the clock to match.
func (h *harness) progress(position int, elapsed time.Duration) {
	h.t.Helper()
	h.clock.Advance(elapsed)
	before := position - int(elapsed/time.Second)
	if before < 0 {
		before = 0
	}
	h.service.HandlePlayback(context.Background(), PlaybackInput{
		UserID:     users.BootstrapUserID,
		Track:      h.track,
		Source:     "playback-patch",
		Before:     catalog.PlaybackState{ProgressSeconds: before},
		After:      catalog.PlaybackState{ProgressSeconds: position},
		Patch:      &playback.PatchInput{ProgressSeconds: &position},
		ObservedAt: h.clock.Now(),
	})
}

// listenThrough plays from `from` to `to` on the client's 20 second timer.
func (h *harness) listenThrough(from, to int) {
	h.t.Helper()
	for position := from + 20; position < to; position += 20 {
		h.progress(position, 20*time.Second)
	}
	h.progress(to, time.Duration(to-from)%20*time.Second+time.Second)
}

func (h *harness) queueSize() int {
	h.t.Helper()
	size, err := countQueue(context.Background(), h.db, users.BootstrapUserID)
	if err != nil {
		h.t.Fatalf("countQueue: %v", err)
	}
	return size
}

func (h *harness) flush() int {
	h.t.Helper()
	flushed, err := h.service.DrainQueue(context.Background(), users.BootstrapUserID, 50, 20)
	if err != nil {
		h.t.Fatalf("DrainQueue: %v", err)
	}
	return flushed
}

func (h *harness) historyStatuses(kind string) []string {
	h.t.Helper()
	page, err := h.service.ListHistory(context.Background(), users.BootstrapUserID, 100, 0)
	if err != nil {
		h.t.Fatalf("ListHistory: %v", err)
	}
	statuses := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		if item.Kind == kind {
			statuses = append(statuses, item.Status)
		}
	}
	return statuses
}

// ---------------------------------------------------------------------------
// listen behaviour, end to end
// ---------------------------------------------------------------------------

// TestRealClientSessionScrobblesExactlyOnce replays the request sequence the
// production logs show for one fully played track.
func TestRealClientSessionScrobblesExactlyOnce(t *testing.T) {
	h := newHarness(t)

	h.stream(0)
	h.listenThrough(0, 160)
	h.clock.Advance(time.Second)
	position := 160
	h.service.HandlePlayback(context.Background(), PlaybackInput{
		UserID: users.BootstrapUserID,
		Track:  h.track,
		Source: "playback-patch",
		Before: catalog.PlaybackState{ProgressSeconds: 160, PlayCount: 2},
		After:  catalog.PlaybackState{ProgressSeconds: 160, PlayCount: 3},
		Patch: &playback.PatchInput{
			IncrementPlayCount: true, TouchLastPlayedAt: true, ProgressSeconds: &position,
		},
		ObservedAt: h.clock.Now(),
	})

	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d, want 1", got)
	}
	call := h.api.of("track.scrobble")[0]
	if call.Get("artist[0]") != "The Static" || call.Get("track[0]") != "Signal One" {
		t.Fatalf("scrobbled %q by %q", call.Get("track[0]"), call.Get("artist[0]"))
	}
	if call.Get("duration[0]") != "161" {
		t.Fatalf("duration = %q, want 161", call.Get("duration[0]"))
	}
	if got, want := call.Get("timestamp[0]"), fmt.Sprint(epoch.Unix()); got != want {
		t.Fatalf("timestamp = %q, want %q (when the track started)", got, want)
	}
	if h.queueSize() != 0 {
		t.Fatalf("queue size = %d after a successful scrobble, want 0", h.queueSize())
	}
}

// TestPressingPlayOnAFinishedTrackDoesNotScrobble is the production defect:
// the saved resume position sits at the end of the track, and opening the
// stream used to be read as a completed listen.
func TestPressingPlayOnAFinishedTrackDoesNotScrobble(t *testing.T) {
	h := newHarness(t)

	h.stream(160) // resume position left by the previous play
	if got := h.api.count("track.scrobble"); got != 0 {
		t.Fatalf("track.scrobble calls = %d on press play, want 0", got)
	}
	if got := h.api.count("track.updateNowPlaying"); got != 1 {
		t.Fatalf("track.updateNowPlaying calls = %d, want 1", got)
	}

	// Playback actually starts over.
	h.clock.Advance(time.Second)
	h.progress(0, 0)
	h.listenThrough(0, 100)
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d after really listening, want 1", got)
	}
}

// TestPressingPlayAnnouncesEvenWhenTheSavedPositionIsAtTheEnd — found driving
// the live server: a client that writes its saved position back before opening
// the stream leaves an open play sitting at the end of the track. Pressing play
// then has to start a new listen, or the listener gets no "now playing" until
// their next position report arrives.
func TestPressingPlayAnnouncesEvenWhenTheSavedPositionIsAtTheEnd(t *testing.T) {
	h := newHarness(t)
	position := 160
	h.service.HandlePlayback(context.Background(), PlaybackInput{
		UserID:     users.BootstrapUserID,
		Track:      h.track,
		Source:     "playback-patch",
		After:      catalog.PlaybackState{ProgressSeconds: position},
		Patch:      &playback.PatchInput{ProgressSeconds: &position},
		ObservedAt: h.clock.Now(),
	})

	h.clock.Advance(2 * time.Second)
	h.stream(160)

	if got := h.api.count("track.updateNowPlaying"); got != 1 {
		t.Fatalf("track.updateNowPlaying calls = %d on press play, want 1", got)
	}
	if got := h.api.count("track.scrobble"); got != 0 {
		t.Fatalf("track.scrobble calls = %d on press play, want 0", got)
	}
}

// TestPlayingATrackTwiceScrobblesTwice covers the silent-miss half of the bug.
func TestPlayingATrackTwiceScrobblesTwice(t *testing.T) {
	h := newHarness(t)

	h.stream(0)
	h.listenThrough(0, 160)
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("first play scrobbles = %d, want 1", got)
	}

	h.clock.Advance(30 * time.Minute)
	h.stream(160)
	h.clock.Advance(time.Second)
	h.progress(0, 0)
	h.listenThrough(0, 160)
	if got := h.api.count("track.scrobble"); got != 2 {
		t.Fatalf("scrobbles = %d after playing the track twice, want 2", got)
	}

	stamps := h.api.of("track.scrobble")
	if stamps[0].Get("timestamp[0]") == stamps[1].Get("timestamp[0]") {
		t.Fatal("the two plays were scrobbled with the same timestamp")
	}
}

// TestConcurrentObservationsScrobbleOnce hammers the service the way the HTTP
// layer does — one goroutine per notification — and requires that only one
// scrobble reaches Last.fm. The ledger, not a mutex, is what guarantees it.
func TestConcurrentObservationsScrobbleOnce(t *testing.T) {
	h := newHarness(t)
	h.stream(0)
	h.listenThrough(0, 60)

	var wg sync.WaitGroup
	at := h.clock.Now().Add(30 * time.Second)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			position := 100 + i
			h.service.HandlePlayback(context.Background(), PlaybackInput{
				UserID:     users.BootstrapUserID,
				Track:      h.track,
				Source:     "playback-patch",
				After:      catalog.PlaybackState{ProgressSeconds: position},
				Patch:      &playback.PatchInput{ProgressSeconds: &position},
				ObservedAt: at,
			})
		}(i)
	}
	wg.Wait()

	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d under concurrency, want exactly 1", got)
	}
}

// TestDuplicateSubmissionIsRefused — the same listen offered twice, however it
// is rediscovered, reaches Last.fm once.
func TestDuplicateSubmissionIsRefused(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	playedAt := epoch.Add(-5 * time.Minute)

	for i := 0; i < 3; i++ {
		if err := h.service.SubmitScrobble(ctx, users.BootstrapUserID, h.track, playedAt, 120, "manual"); err != nil {
			t.Fatalf("SubmitScrobble %d: %v", i, err)
		}
	}
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d for the same listen submitted three times, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// delivery
// ---------------------------------------------------------------------------

// TestOutageQueuesThenDeliversExactlyOnce — a listen earned while Last.fm is
// unreachable is durable, and goes out once the outage ends.
func TestOutageQueuesThenDeliversExactlyOnce(t *testing.T) {
	h := newHarness(t)
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "track.scrobble" {
			return http.StatusBadGateway, "upstream down"
		}
		return 0, ""
	})

	h.stream(0)
	h.listenThrough(0, 100)
	if h.queueSize() != 1 {
		t.Fatalf("queue size = %d during an outage, want 1", h.queueSize())
	}

	h.api.setReply(nil)
	h.api.reset()
	h.clock.Advance(2 * time.Minute)
	if flushed := h.flush(); flushed != 1 {
		t.Fatalf("flushed = %d, want 1", flushed)
	}
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d after recovery, want 1", got)
	}
	if h.queueSize() != 0 {
		t.Fatalf("queue size = %d after delivery, want 0", h.queueSize())
	}

	// Flushing again must not resend it.
	h.clock.Advance(time.Hour)
	if flushed := h.flush(); flushed != 0 {
		t.Fatalf("a second flush delivered %d items, want 0", flushed)
	}
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d after a second flush, want 1", got)
	}
}

// TestLongOutageNeverDiscardsAListen. The old policy gave up after eight
// attempts one minute apart, so any outage longer than about nine minutes
// silently deleted every listen inside it.
func TestLongOutageNeverDiscardsAListen(t *testing.T) {
	h := newHarness(t)
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "track.scrobble" {
			return http.StatusServiceUnavailable, "maintenance"
		}
		return 0, ""
	})

	h.stream(0)
	h.listenThrough(0, 100)

	// Twelve failed retries spread over several hours.
	for i := 0; i < 12; i++ {
		h.clock.Advance(3 * time.Hour)
		h.flush()
		if h.queueSize() != 1 {
			t.Fatalf("the listen was discarded after %d failed attempts", i+1)
		}
	}

	h.api.setReply(nil)
	h.api.reset()
	h.clock.Advance(3 * time.Hour)
	if flushed := h.flush(); flushed != 1 {
		t.Fatalf("flushed = %d once the outage ended, want 1", flushed)
	}
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d, want 1", got)
	}
}

// TestScrobbleIsDurableBeforeLastFMIsCalled — the queue row exists even when
// the process never gets a reply, so a crash mid-delivery loses nothing. A
// second Service standing in for the restarted server delivers it.
func TestScrobbleIsDurableBeforeLastFMIsCalled(t *testing.T) {
	h := newHarness(t)
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "track.scrobble" {
			return http.StatusGatewayTimeout, "gone"
		}
		return 0, ""
	})

	h.stream(0)
	h.listenThrough(0, 100)
	if h.queueSize() != 1 {
		t.Fatalf("queue size = %d, want the listen persisted before delivery", h.queueSize())
	}

	h.api.setReply(nil)
	h.api.reset()
	restarted := newTestService(t, h.db, h.api.server, func(options *ServiceOptions) {
		options.Now = h.clock.Now
	})
	h.clock.Advance(10 * time.Minute)
	flushed, err := restarted.DrainQueue(context.Background(), "", 50, 5)
	if err != nil {
		t.Fatalf("DrainQueue: %v", err)
	}
	if flushed != 1 {
		t.Fatalf("a restarted server delivered %d items, want 1", flushed)
	}
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d, want 1", got)
	}
}

// TestIgnoredScrobbleIsDroppedNotRetriedForever — Last.fm answers 200 and then
// says it declined the scrobble in the body. The old code never looked.
func TestIgnoredScrobbleIsDroppedNotRetriedForever(t *testing.T) {
	h := newHarness(t)
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "track.scrobble" {
			return http.StatusOK, `{"scrobbles":{"@attr":{"accepted":0,"ignored":1},` +
				`"scrobble":{"ignoredMessage":{"code":"3","#text":"Timestamp too old"}}}}`
		}
		return 0, ""
	})

	h.stream(0)
	h.listenThrough(0, 100)

	if h.queueSize() != 0 {
		t.Fatalf("queue size = %d, want 0 (a rejected scrobble must not be retried forever)", h.queueSize())
	}
	statuses := h.historyStatuses(queueKindScrobble)
	if len(statuses) != 1 || statuses[0] != submissionStatusDropped {
		t.Fatalf("scrobble history = %v, want one dropped entry", statuses)
	}
}

// TestScrobbleRetriesWithoutMusicBrainzIDWhenRejected — an unresolvable
// MusicBrainz id makes Last.fm reject an otherwise fine listen. The plain
// artist/track form always matches, so fall back rather than lose it.
func TestScrobbleRetriesWithoutMusicBrainzIDWhenRejected(t *testing.T) {
	h := newHarness(t)
	h.track.ExternalIDs.MusicBrainzRecordingID = "9d1b0f4c-0000-4000-8000-000000000000"
	h.api.setReply(func(method string, form url.Values) (int, string) {
		if method != "track.scrobble" {
			return 0, ""
		}
		if form.Get("mbid[0]") != "" {
			return http.StatusOK, `{"scrobbles":{"@attr":{"accepted":0,"ignored":1},` +
				`"scrobble":{"ignoredMessage":{"code":"2","#text":"Track was ignored"}}}}`
		}
		return 0, ""
	})

	h.stream(0)
	h.listenThrough(0, 100)

	calls := h.api.of("track.scrobble")
	if len(calls) != 2 {
		t.Fatalf("track.scrobble calls = %d, want 2 (one with the mbid, one without)", len(calls))
	}
	if calls[0].Get("mbid[0]") == "" || calls[1].Get("mbid[0]") != "" {
		t.Fatalf("expected the retry to drop the mbid: %q then %q", calls[0].Get("mbid[0]"), calls[1].Get("mbid[0]"))
	}
	if h.queueSize() != 0 {
		t.Fatalf("queue size = %d, want 0 after the fallback succeeded", h.queueSize())
	}
}

// TestAuthFailureClearsTheSessionAndHoldsTheListen — a dead session key must
// not cost the user their listens.
func TestAuthFailureClearsTheSessionAndHoldsTheListen(t *testing.T) {
	h := newHarness(t)
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "track.scrobble" {
			return http.StatusOK, `{"error":9,"message":"Invalid session key"}`
		}
		return 0, ""
	})

	h.stream(0)
	h.listenThrough(0, 100)

	if h.queueSize() != 1 {
		t.Fatalf("queue size = %d, want the listen held for re-auth", h.queueSize())
	}
	if _, err := loadSession(context.Background(), h.db, users.BootstrapUserID); err == nil {
		t.Fatal("expected the dead session to be cleared")
	}

	// Reconnecting delivers what was held.
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "auth.getSession" {
			return http.StatusOK, `{"session":{"name":"jake","key":"fresh-session-key"}}`
		}
		return 0, ""
	})
	h.api.reset()
	if _, err := h.service.CompleteAuth(context.Background(), users.BootstrapUserID, "token-123"); err != nil {
		t.Fatalf("CompleteAuth: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool { return h.queueSize() == 0 })
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d after reconnecting, want 1", got)
	}
}

// TestNowPlayingIsNeverQueued — replaying a stale "now playing" announces the
// wrong song, so a failed one is simply dropped.
func TestNowPlayingIsNeverQueued(t *testing.T) {
	h := newHarness(t)
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "track.updateNowPlaying" {
			return http.StatusBadGateway, "down"
		}
		return 0, ""
	})

	h.stream(0)
	h.progress(20, 20*time.Second)

	if h.queueSize() != 0 {
		t.Fatalf("queue size = %d, want 0 (now playing must never be queued)", h.queueSize())
	}
}

// TestManualRetryIgnoresTheBackoffSchedule — pressing retry must act now, not
// whenever the exponential backoff next comes due.
func TestManualRetryIgnoresTheBackoffSchedule(t *testing.T) {
	h := newHarness(t)
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "track.scrobble" {
			return http.StatusBadGateway, "down"
		}
		return 0, ""
	})

	h.stream(0)
	h.listenThrough(0, 100)
	// Fail it enough times to push the retry hours out.
	for i := 0; i < 6; i++ {
		h.clock.Advance(3 * time.Hour)
		h.flush()
	}
	if h.queueSize() != 1 {
		t.Fatalf("queue size = %d, want 1", h.queueSize())
	}

	h.api.setReply(nil)
	h.api.reset()
	flushed, err := h.service.RetryQueue(context.Background(), users.BootstrapUserID, 50)
	if err != nil {
		t.Fatalf("RetryQueue: %v", err)
	}
	if flushed != 1 {
		t.Fatalf("RetryQueue delivered %d, want 1 without waiting out the backoff", flushed)
	}
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d, want 1", got)
	}
}

// TestPollerDeliversTheBacklog — the background poller is what eventually
// rescues everything, and it must run even when Last.fm was not configured at
// the moment the process started.
func TestPollerDeliversTheBacklog(t *testing.T) {
	h := newHarness(t)
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "track.scrobble" {
			return http.StatusBadGateway, "down"
		}
		return 0, ""
	})
	h.stream(0)
	h.listenThrough(0, 100)
	if h.queueSize() != 1 {
		t.Fatalf("queue size = %d, want 1", h.queueSize())
	}

	h.api.setReply(nil)
	h.api.reset()
	h.clock.Advance(5 * time.Minute)

	poller := NewPoller(PollerOptions{Service: h.service, Tick: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = poller.Run(ctx) }()

	waitFor(t, 5*time.Second, func() bool { return h.queueSize() == 0 })
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d, want exactly 1", got)
	}
}

// TestNowPlayingRetriesQuietlyThenRecovers — a failed "now playing" is retried
// on the listener's next position report rather than queued, but it is audited
// only once, so a long outage does not fill the history with one row per report.
func TestNowPlayingRetriesQuietlyThenRecovers(t *testing.T) {
	h := newHarness(t)
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "track.updateNowPlaying" {
			return http.StatusBadGateway, "down"
		}
		return 0, ""
	})

	h.stream(0)
	h.progress(20, 20*time.Second)
	h.progress(40, 20*time.Second)
	h.progress(60, 20*time.Second)

	if got := h.api.count("track.updateNowPlaying"); got < 3 {
		t.Fatalf("track.updateNowPlaying attempts = %d, want a retry on each report", got)
	}
	if statuses := h.historyStatuses(queueKindNowPlaying); len(statuses) != 1 {
		t.Fatalf("now playing history rows = %d, want 1 for a run of identical failures", len(statuses))
	}
	if h.queueSize() != 0 {
		t.Fatalf("queue size = %d, want 0 (now playing is never queued)", h.queueSize())
	}

	// Once Last.fm answers again the announcement lands, and the refresh
	// throttle takes over so it is not resent on every report.
	h.api.setReply(nil)
	h.api.reset()
	h.progress(80, 20*time.Second)
	h.progress(100, 20*time.Second)
	if got := h.api.count("track.updateNowPlaying"); got != 1 {
		t.Fatalf("track.updateNowPlaying calls = %d after recovery, want 1 then throttled", got)
	}
}

// TestFlushDeliversOldestListenFirst keeps a recovered backlog in the order it
// was heard.
func TestFlushDeliversOldestListenFirst(t *testing.T) {
	h := newHarness(t)
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "track.scrobble" {
			return http.StatusBadGateway, "down"
		}
		return 0, ""
	})

	ctx := context.Background()
	for i := 3; i >= 1; i-- {
		track := h.track
		track.ID = fmt.Sprintf("track-%d", i)
		track.Title = fmt.Sprintf("Song %d", i)
		if err := h.service.SubmitScrobble(ctx, users.BootstrapUserID, track,
			epoch.Add(-time.Duration(i)*time.Hour), 120, "manual"); err != nil {
			t.Fatalf("SubmitScrobble: %v", err)
		}
	}

	h.api.setReply(nil)
	h.api.reset()
	h.clock.Advance(5 * time.Minute)
	if flushed := h.flush(); flushed != 3 {
		t.Fatalf("flushed = %d, want 3", flushed)
	}
	titles := make([]string, 0, 3)
	for _, call := range h.api.of("track.scrobble") {
		titles = append(titles, call.Get("track[0]"))
	}
	want := []string{"Song 3", "Song 2", "Song 1"} // oldest listen first
	if strings.Join(titles, ",") != strings.Join(want, ",") {
		t.Fatalf("delivery order = %v, want %v", titles, want)
	}
}

// ---------------------------------------------------------------------------
// existing behaviour that must keep working
// ---------------------------------------------------------------------------

func TestHandleScrobbleEventComplete(t *testing.T) {
	h := newHarness(t)
	response, err := h.service.HandleScrobbleEvent(context.Background(), users.BootstrapUserID, h.track, ScrobbleEventInput{
		TrackID:         "track-1",
		Event:           "complete",
		ProgressSeconds: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Scrobbled {
		t.Fatalf("response = %+v, want scrobbled", response)
	}
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d, want 1", got)
	}
}

// TestSkipBeforeThresholdDoesNotScrobbleOrAffectTheNextPlay — abandoning a
// track early records nothing, and leaves no state behind to spoil the next
// listen.
func TestSkipBeforeThresholdDoesNotScrobbleOrAffectTheNextPlay(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.service.HandleScrobbleEvent(ctx, users.BootstrapUserID, h.track, ScrobbleEventInput{
		TrackID: "track-1", Event: "start",
	}); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(40 * time.Second)
	if _, err := h.service.HandleScrobbleEvent(ctx, users.BootstrapUserID, h.track, ScrobbleEventInput{
		TrackID: "track-1", Event: "skip", ProgressSeconds: 40,
	}); err != nil {
		t.Fatal(err)
	}
	if got := h.api.count("track.scrobble"); got != 0 {
		t.Fatalf("track.scrobble calls = %d after an early skip, want 0", got)
	}

	// Coming back to the track later still scrobbles normally.
	h.clock.Advance(time.Minute)
	h.stream(0)
	h.listenThrough(0, 100)
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d after really listening, want 1", got)
	}
}

// TestSkipAfterThresholdKeepsTheListen — Last.fm counts a play the moment the
// threshold is met, so hitting next near the end of a track does not take it
// back. Skipping only prevents a scrobble that was never earned.
func TestSkipAfterThresholdKeepsTheListen(t *testing.T) {
	h := newHarness(t)
	h.stream(0)
	h.listenThrough(0, 100)
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d, want 1", got)
	}
	skipped := 101
	h.clock.Advance(time.Second)
	h.service.HandlePlayback(context.Background(), PlaybackInput{
		UserID:     users.BootstrapUserID,
		Track:      h.track,
		Source:     "playback-patch",
		After:      catalog.PlaybackState{ProgressSeconds: skipped},
		Patch:      &playback.PatchInput{IncrementSkipCount: true, ProgressSeconds: &skipped},
		ObservedAt: h.clock.Now(),
	})
	if got := h.api.count("track.scrobble"); got != 1 {
		t.Fatalf("track.scrobble calls = %d after the skip, want 1", got)
	}
}

func TestLoveTrackOnFavoriteChange(t *testing.T) {
	h := newHarness(t)
	favorite := true
	h.service.HandlePlayback(context.Background(), PlaybackInput{
		UserID:     users.BootstrapUserID,
		Track:      h.track,
		Source:     "playback-patch",
		After:      catalog.PlaybackState{Favorite: true},
		Patch:      &playback.PatchInput{Favorite: &favorite, ProgressSeconds: intPtr(10), TouchLastPositionAt: true},
		ObservedAt: h.clock.Now(),
	})
	if got := h.api.count("track.love"); got != 1 {
		t.Fatalf("track.love calls = %d, want 1", got)
	}
}

func TestLoveIsQueuedAndRetriedWhenUpstreamFails(t *testing.T) {
	h := newHarness(t)
	h.api.setReply(func(method string, _ url.Values) (int, string) {
		if method == "track.love" {
			return http.StatusBadGateway, "down"
		}
		return 0, ""
	})
	favorite := true
	h.service.HandlePlayback(context.Background(), PlaybackInput{
		UserID:     users.BootstrapUserID,
		Track:      h.track,
		Source:     "playback-patch",
		After:      catalog.PlaybackState{Favorite: true},
		Patch:      &playback.PatchInput{Favorite: &favorite, ProgressSeconds: intPtr(10)},
		ObservedAt: h.clock.Now(),
	})
	if h.queueSize() != 1 {
		t.Fatalf("queue size = %d, want the love held for retry", h.queueSize())
	}

	h.api.setReply(nil)
	h.clock.Advance(5 * time.Minute)
	if flushed := h.flush(); flushed != 1 {
		t.Fatalf("flushed = %d, want 1", flushed)
	}
}

func TestScrobblesUseSeparateUserSessions(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	api := newFakeLastFM(t)

	userService := users.New(users.ServiceOptions{DB: db})
	if err := userService.Bootstrap(ctx, users.BootstrapInput{AdminUsername: "owner", AdminPassword: "owner-pass-123"}); err != nil {
		t.Fatal(err)
	}
	owner, err := userService.AuthenticateCredentials(ctx, "owner", "owner-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := userService.Create(ctx, owner, users.CreateUserInput{
		Username: "listener", Password: "listener-pass-123", Role: users.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	seedLastFMSessionForUser(t, db, owner.User.ID, "one", "session-one")
	seedLastFMSessionForUser(t, db, listener.ID, "two", "session-two")
	service := newTestService(t, db, api.server, nil)

	if err := service.SubmitScrobble(ctx, owner.User.ID, trackOf(161), time.Unix(1000, 0), 0, "native-test"); err != nil {
		t.Fatal(err)
	}
	if err := service.SubmitScrobble(ctx, listener.ID, trackOf(161), time.Unix(2000, 0), 0, "native-test"); err != nil {
		t.Fatal(err)
	}
	calls := api.of("track.scrobble")
	if len(calls) != 2 || calls[0].Get("sk") != "session-one" || calls[1].Get("sk") != "session-two" {
		t.Fatalf("session keys = %#v", calls)
	}
	one, err := service.ListHistory(ctx, owner.User.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	two, err := service.ListHistory(ctx, listener.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if one.Total != 1 || two.Total != 1 {
		t.Fatalf("history totals: one=%d two=%d", one.Total, two.Total)
	}
}

func TestSignParamsMatchesLastFMRules(t *testing.T) {
	sig := signParams("test-secret", map[string]string{
		"method": "auth.getToken", "api_key": "test-key", "format": "json",
	})
	if len(sig) != 32 {
		t.Fatalf("signature length = %d, want 32", len(sig))
	}
}

func TestGetSessionSignature(t *testing.T) {
	params := map[string]string{
		"api_key": "key", "format": "json", "method": "auth.getSession", "token": "token-123",
	}
	sig := signParams("secret", params)
	if sig != signParams("secret", map[string]string{
		"api_key": "key", "method": "auth.getSession", "token": "token-123",
	}) {
		t.Fatal("signature must ignore format")
	}
	// Pin the exact md5 to the real Last.fm algorithm (name+value sorted, then
	// secret appended ONCE): md5("api_keykeymethodauth.getSessiontokentoken-123secret").
	// Guards the error-13 regression where the secret was also prepended.
	const want = "5e618b4c044fd0547a24e5f3869d5403"
	if sig != want {
		t.Fatalf("api_sig = %q, want %q (Last.fm sign = params+secret, secret only at end)", sig, want)
	}
}

func TestSaveConfigRequiresSecretWhenAPIKeyChanges(t *testing.T) {
	ctx := context.Background()
	api := newFakeLastFM(t)
	api.setReply(func(string, url.Values) (int, string) { return http.StatusOK, `{"token":"ok"}` })

	service := newTestService(t, openTestDB(t), api.server, nil)
	if _, err := service.SaveConfig(ctx, AppConfigInput{APIKey: "original-key", SharedSecret: "original-secret"}); err != nil {
		t.Fatalf("initial SaveConfig: %v", err)
	}
	_, err := service.SaveConfig(ctx, AppConfigInput{APIKey: "new-key"})
	if err == nil {
		t.Fatal("expected error when changing api key without shared secret")
	}
	if !strings.Contains(err.Error(), "shared secret is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestCompleteAuthStoresSession(t *testing.T) {
	ctx := context.Background()
	api := newFakeLastFM(t)
	api.setReply(func(method string, form url.Values) (int, string) {
		if method != "auth.getSession" {
			return 0, ""
		}
		expected := signParams("secret", map[string]string{
			"api_key": "key", "method": "auth.getSession", "token": "token-123",
		})
		if form.Get("api_sig") != expected {
			return http.StatusOK, `{"error":13,"message":"Invalid method signature supplied"}`
		}
		return http.StatusOK, `{"session":{"name":"jake","key":"session-key","subscriber":0}}`
	})

	service := newTestService(t, openTestDB(t), api.server, nil)
	response, err := service.CompleteAuth(ctx, users.BootstrapUserID, "token-123")
	if err != nil {
		t.Fatal(err)
	}
	if response.Username != "jake" || !response.Connected {
		t.Fatalf("response = %+v", response)
	}
}

func TestLastFMStatusJSON(t *testing.T) {
	now := time.Now().UTC()
	status := Status{Enabled: true, Connected: true, Username: "jake", QueueSize: 2, ConnectedAt: &now}
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"queueSize":2`) {
		t.Fatalf("payload = %s", payload)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestService(t *testing.T, db *sql.DB, server *httptest.Server, configure func(*ServiceOptions)) *Service {
	t.Helper()
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			req.URL.Scheme = "http"
			req.URL.Host = strings.TrimPrefix(server.URL, "http://")
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
	options := ServiceOptions{
		DB:           db,
		APIKey:       "key",
		SharedSecret: "secret",
		HTTPClient:   httpClient,
		Logger:       func(string, ...any) {},
	}
	if configure != nil {
		configure(&options)
	}
	service := NewService(options)
	// Retry backoff inside a single request costs real wall time; tests do not
	// need to wait it out.
	service.client.sleep = func(time.Duration) {}
	return service
}

func waitFor(t *testing.T, limit time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", limit)
}

func seedLastFMSession(t *testing.T, db *sql.DB) {
	t.Helper()
	seedLastFMSessionForUser(t, db, users.BootstrapUserID, "jake", "session-key")
}

func seedLastFMSessionForUser(t *testing.T, db *sql.DB, userID, username, sessionKey string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO lastfm_user_settings (user_id, lastfm_username, session_key, connected_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, userID, username, sessionKey); err != nil {
		t.Fatal(err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return storagetest.Open(t)
}

func intPtr(value int) *int { return &value }
