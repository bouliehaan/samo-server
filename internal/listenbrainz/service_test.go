package listenbrainz

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/scrobble"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

// epoch anchors every clock in this suite.
var epoch = time.Date(2026, 8, 14, 21, 5, 0, 0, time.UTC)

const testToken = "lb-token-0001"

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

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

// submitCall is one recorded POST /1/submit-listens.
type submitCall struct {
	ListenType string
	Listens    []listenItem
	Token      string
}

// fakeListenBrainz stands in for the real service and lets a test choose the
// reply per call.
type fakeListenBrainz struct {
	mu      sync.Mutex
	server  *httptest.Server
	submits []submitCall
	valid   []string
	// reply chooses the response for a submit. Nil means 200 OK.
	reply func(call submitCall) (int, string)
	// tokenValid controls validate-token.
	tokenValid bool
	username   string
}

func newFakeListenBrainz(t *testing.T) *fakeListenBrainz {
	t.Helper()
	api := &fakeListenBrainz{tokenValid: true, username: "jake"}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Token ")
		switch r.URL.Path {
		case "/1/validate-token":
			api.mu.Lock()
			api.valid = append(api.valid, token)
			valid, username := api.tokenValid, api.username
			api.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if !valid {
				fmt.Fprint(w, `{"code":200,"message":"Token invalid.","valid":false}`)
				return
			}
			fmt.Fprintf(w, `{"code":200,"message":"Token valid.","valid":true,"user_name":%q}`, username)

		case "/1/submit-listens":
			var request submitRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `{"code":400,"error":%q}`, err.Error())
				return
			}
			call := submitCall{ListenType: request.ListenType, Listens: request.Payload, Token: token}
			api.mu.Lock()
			api.submits = append(api.submits, call)
			reply := api.reply
			api.mu.Unlock()

			status, body := http.StatusOK, `{"status":"ok"}`
			if reply != nil {
				status, body = reply(call)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			fmt.Fprint(w, body)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(api.server.Close)
	return api
}

func (a *fakeListenBrainz) setReply(fn func(call submitCall) (int, string)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reply = fn
}

func (a *fakeListenBrainz) setTokenValid(valid bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokenValid = valid
}

// of returns every submit of one listen_type.
func (a *fakeListenBrainz) of(listenType string) []submitCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	matched := make([]submitCall, 0, len(a.submits))
	for _, call := range a.submits {
		if call.ListenType == listenType {
			matched = append(matched, call)
		}
	}
	return matched
}

// listensSubmitted counts individual listens across every real submission,
// which is what "did this scrobble" actually means.
func (a *fakeListenBrainz) listensSubmitted() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	total := 0
	for _, call := range a.submits {
		if call.ListenType == listenTypePlayingNow {
			continue
		}
		total += len(call.Listens)
	}
	return total
}

func (a *fakeListenBrainz) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.submits = nil
}

type harness struct {
	t       *testing.T
	db      *sql.DB
	api     *fakeListenBrainz
	clock   *clock
	service *Service
	track   catalog.MusicTrack
	plays   int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := storagetest.Open(t)
	api := newFakeListenBrainz(t)
	c := newClock()
	h := &harness{t: t, db: db, api: api, clock: c, track: trackOf(161)}
	h.service = NewService(ServiceOptions{
		DB:      db,
		APIRoot: api.server.URL,
		Logger:  func(string, ...any) {},
		Now:     c.Now,
		NewPlayID: func() string {
			h.plays++
			return fmt.Sprintf("play-%d", h.plays)
		},
		Sleep: func(time.Duration) {},
	})
	return h
}

// connect performs the real Connect flow, which is how every test gets a token.
func (h *harness) connect() {
	h.t.Helper()
	if _, err := h.service.Connect(context.Background(), users.BootstrapUserID, ConnectInput{Token: testToken}); err != nil {
		h.t.Fatalf("connect: %v", err)
	}
	h.api.reset()
}

// stream replays `GET /music/tracks/{id}/stream`.
func (h *harness) stream(resume int) {
	h.t.Helper()
	h.service.HandlePlayback(context.Background(), PlaybackInput{
		UserID:     users.BootstrapUserID,
		Track:      h.track,
		Source:     scrobble.SourceStream,
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
		ObservedAt: h.clock.Now(),
	})
}

// listenThrough plays from `from` to `to` in the 20s steps real clients use.
func (h *harness) listenThrough(from, to int) {
	h.t.Helper()
	for position := from + 20; position <= to; position += 20 {
		h.progress(position, 20*time.Second)
	}
}

func (h *harness) queueSize() int {
	h.t.Helper()
	size, err := countQueue(context.Background(), h.db, users.BootstrapUserID)
	if err != nil {
		h.t.Fatalf("count queue: %v", err)
	}
	return size
}

func trackOf(seconds int) catalog.MusicTrack {
	return catalog.MusicTrack{
		ID:               "track-1",
		Title:            "Signal One",
		ArtistNames:      []string{"The Static"},
		AlbumTitle:       "Night Broadcasts",
		AlbumArtistNames: []string{"The Static"},
		TrackNumber:      3,
		DurationSeconds:  seconds,
		ExternalIDs: catalog.ExternalIDs{
			MusicBrainzRecordingID: "rec-mbid-1",
			MusicBrainzReleaseID:   "rel-mbid-1",
			MusicBrainzArtistID:    "art-mbid-1",
		},
	}
}

// ---------------------------------------------------------------------------
// connection
// ---------------------------------------------------------------------------

func TestConnectValidatesTheTokenAndStoresIt(t *testing.T) {
	h := newHarness(t)

	response, err := h.service.Connect(context.Background(), users.BootstrapUserID, ConnectInput{Token: testToken})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if response.Username != "jake" {
		t.Fatalf("username = %q, want %q", response.Username, "jake")
	}
	if !response.Connected {
		t.Fatal("connect did not report a connection")
	}

	status, err := h.service.Status(context.Background(), users.BootstrapUserID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Connected || status.Username != "jake" {
		t.Fatalf("status = %+v, want connected as jake", status)
	}
}

// A rejected token must fail loudly at connect time. Storing it anyway would
// hide the mistake until a day of listens had silently failed to deliver.
func TestConnectRejectsAnInvalidToken(t *testing.T) {
	h := newHarness(t)
	h.api.setTokenValid(false)

	if _, err := h.service.Connect(context.Background(), users.BootstrapUserID, ConnectInput{Token: "nope"}); err != ErrInvalidToken {
		t.Fatalf("connect error = %v, want %v", err, ErrInvalidToken)
	}
	status, _ := h.service.Status(context.Background(), users.BootstrapUserID)
	if status.Connected {
		t.Fatal("an invalid token was stored as a connection")
	}
}

func TestConnectRejectsAMalformedAPIRoot(t *testing.T) {
	h := newHarness(t)
	_, err := h.service.Connect(context.Background(), users.BootstrapUserID, ConnectInput{
		Token:   testToken,
		APIRoot: "not-a-url",
	})
	if err != ErrInvalidRoot {
		t.Fatalf("connect error = %v, want %v", err, ErrInvalidRoot)
	}
}

// The integration must stand on its own: no application key, no operator
// configuration, nothing borrowed from Last.fm.
func TestServiceIsAvailableWithoutAnyServerConfiguration(t *testing.T) {
	db := storagetest.Open(t)
	service := NewService(ServiceOptions{DB: db, Logger: func(string, ...any) {}})
	if !service.Enabled() {
		t.Fatal("listenbrainz must be available without operator configuration")
	}
	if got := service.DefaultAPIRoot(); got != DefaultAPIRoot {
		t.Fatalf("default api root = %q, want %q", got, DefaultAPIRoot)
	}
}

// ---------------------------------------------------------------------------
// listens
// ---------------------------------------------------------------------------

func TestListeningPastTheThresholdSubmitsOneListen(t *testing.T) {
	h := newHarness(t)
	h.connect()

	h.stream(0)
	h.listenThrough(0, 100)

	if got := h.api.listensSubmitted(); got != 1 {
		t.Fatalf("listens submitted = %d, want 1", got)
	}
	call := h.api.of(listenTypeSingle)
	if len(call) != 1 {
		t.Fatalf("single submissions = %d, want 1", len(call))
	}
	listen := call[0].Listens[0]
	if listen.ListenedAt != epoch.Unix() {
		t.Fatalf("listened_at = %d, want %d (the moment the track started)", listen.ListenedAt, epoch.Unix())
	}
	if listen.TrackMetadata.ArtistName != "The Static" || listen.TrackMetadata.TrackName != "Signal One" {
		t.Fatalf("track metadata = %+v", listen.TrackMetadata)
	}
	if listen.TrackMetadata.ReleaseName != "Night Broadcasts" {
		t.Fatalf("release_name = %q", listen.TrackMetadata.ReleaseName)
	}
	info := listen.TrackMetadata.AdditionalInfo
	if info == nil {
		t.Fatal("additional_info missing")
	}
	if info.RecordingMBID != "rec-mbid-1" || info.ReleaseMBID != "rel-mbid-1" {
		t.Fatalf("mbids = %+v, want the catalog's", info)
	}
	if len(info.ArtistMBIDs) != 1 || info.ArtistMBIDs[0] != "art-mbid-1" {
		t.Fatalf("artist_mbids = %v", info.ArtistMBIDs)
	}
	if info.DurationMS != 161000 {
		t.Fatalf("duration_ms = %d, want 161000", info.DurationMS)
	}
	if info.TrackNumber != 3 {
		t.Fatalf("tracknumber = %d, want 3", info.TrackNumber)
	}
	if call[0].Token != testToken {
		t.Fatalf("submitted with token %q, want %q", call[0].Token, testToken)
	}
	if h.queueSize() != 0 {
		t.Fatalf("queue = %d after a successful submit, want 0", h.queueSize())
	}
}

// Resuming a track at its saved end position is not a listen. This is the
// defect that dominated the Last.fm logs; the shared engine prevents it here
// too, and this test proves the wiring actually consults the engine.
func TestResumeAtEndOfTrackSubmitsNothing(t *testing.T) {
	h := newHarness(t)
	h.connect()

	h.stream(161)

	if got := h.api.listensSubmitted(); got != 0 {
		t.Fatalf("listens submitted = %d, want 0 (a resume position is not a listen)", got)
	}
}

// A short track can never earn a listen, however long it is left open.
func TestShortTrackNeverSubmits(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.track = trackOf(20)

	h.stream(0)
	h.listenThrough(0, 20)

	if got := h.api.listensSubmitted(); got != 0 {
		t.Fatalf("listens submitted = %d, want 0 for a 20s track", got)
	}
}

// Exactly-once is the whole point of the ledger: replaying the same play — a
// re-sent request, a duplicate notification — must never submit twice.
func TestTheSamePlayIsNeverSubmittedTwice(t *testing.T) {
	h := newHarness(t)
	h.connect()

	h.stream(0)
	h.listenThrough(0, 100)
	if got := h.api.listensSubmitted(); got != 1 {
		t.Fatalf("listens submitted = %d, want 1", got)
	}

	// Keep reporting the same play: the end-of-track position bump, and a
	// repeat of an earlier observation.
	h.listenThrough(100, 160)
	h.progress(160, 0)

	if got := h.api.listensSubmitted(); got != 1 {
		t.Fatalf("listens submitted = %d after continued reports, want 1", got)
	}
}

// Playing the same track a second time IS a second listen. The old Last.fm
// latch got this wrong, so it is worth proving separately here.
func TestPlayingATrackTwiceSubmitsTwice(t *testing.T) {
	h := newHarness(t)
	h.connect()

	h.stream(0)
	h.listenThrough(0, 100)
	h.clock.Advance(time.Minute)
	h.stream(0)
	h.listenThrough(0, 100)

	if got := h.api.listensSubmitted(); got != 2 {
		t.Fatalf("listens submitted = %d, want 2", got)
	}
}

// ---------------------------------------------------------------------------
// playing now
// ---------------------------------------------------------------------------

func TestPlayingNowIsAnnouncedWithoutATimestamp(t *testing.T) {
	h := newHarness(t)
	h.connect()

	h.stream(0)

	calls := h.api.of(listenTypePlayingNow)
	if len(calls) != 1 {
		t.Fatalf("playing_now submissions = %d, want 1", len(calls))
	}
	listen := calls[0].Listens[0]
	if listen.ListenedAt != 0 {
		t.Fatalf("playing_now carried listened_at = %d, want it omitted", listen.ListenedAt)
	}
	if listen.TrackMetadata.TrackName != "Signal One" {
		t.Fatalf("playing_now track = %q", listen.TrackMetadata.TrackName)
	}
	// duration_played describes a listen that happened; "playing now" has not.
	if info := listen.TrackMetadata.AdditionalInfo; info != nil && info.DurationPlayed != 0 {
		t.Fatalf("playing_now carried duration_played = %d, want 0", info.DurationPlayed)
	}
}

// A failed playing_now must never be queued: by the time a retry landed the
// listener would be on a different track.
func TestPlayingNowIsNeverQueued(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.api.setReply(func(call submitCall) (int, string) {
		if call.ListenType == listenTypePlayingNow {
			return http.StatusInternalServerError, `{"code":500,"error":"boom"}`
		}
		return http.StatusOK, `{"status":"ok"}`
	})

	h.stream(0)

	if h.queueSize() != 0 {
		t.Fatalf("queue = %d after a failed playing_now, want 0", h.queueSize())
	}
}

// ---------------------------------------------------------------------------
// durability
// ---------------------------------------------------------------------------

// An outage must not lose a listen: it is queued, and delivered when the
// service comes back.
func TestATransientFailureQueuesTheListenAndFlushDeliversIt(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.api.setReply(func(submitCall) (int, string) {
		return http.StatusInternalServerError, `{"code":500,"error":"down"}`
	})

	h.stream(0)
	h.listenThrough(0, 100)

	if h.queueSize() != 1 {
		t.Fatalf("queue = %d after an outage, want 1", h.queueSize())
	}

	// The service recovers, and the retry backoff elapses.
	h.api.setReply(nil)
	h.api.reset()
	h.clock.Advance(2 * time.Hour)

	delivered, err := h.service.FlushQueue(context.Background(), users.BootstrapUserID, 50)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("flushed %d, want 1", delivered)
	}
	if h.queueSize() != 0 {
		t.Fatalf("queue = %d after a successful flush, want 0", h.queueSize())
	}
}

// A listen recovered from the queue days later keeps the time it happened.
// ListenBrainz accepts historical listens, so clamping it to "now" — which
// Last.fm's two-week rule forces — would file it under the wrong day.
func TestAQueuedListenKeepsItsOriginalTimestamp(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.api.setReply(func(submitCall) (int, string) {
		return http.StatusServiceUnavailable, `{"code":503,"error":"down"}`
	})

	h.stream(0)
	h.listenThrough(0, 100)

	h.api.setReply(nil)
	h.api.reset()
	h.clock.Advance(30 * 24 * time.Hour)

	if _, err := h.service.FlushQueue(context.Background(), users.BootstrapUserID, 50); err != nil {
		t.Fatalf("flush: %v", err)
	}
	calls := h.api.of(listenTypeSingle)
	if len(calls) != 1 {
		t.Fatalf("submissions = %d, want 1", len(calls))
	}
	if got := calls[0].Listens[0].ListenedAt; got != epoch.Unix() {
		t.Fatalf("listened_at = %d, want %d (a month-old listen keeps its own time)", got, epoch.Unix())
	}
}

// A payload ListenBrainz will never accept must be dropped, not retried
// forever, or it wedges every listen behind it.
func TestAPermanentRejectionDropsTheListen(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.api.setReply(func(call submitCall) (int, string) {
		if call.ListenType == listenTypePlayingNow {
			return http.StatusOK, `{"status":"ok"}`
		}
		return http.StatusBadRequest, `{"code":400,"error":"invalid listen"}`
	})

	h.stream(0)
	h.listenThrough(0, 100)

	if h.queueSize() != 0 {
		t.Fatalf("queue = %d after a permanent rejection, want 0 (it must be dropped)", h.queueSize())
	}
}

// A revoked token must hold the listens rather than discard them: pasting a
// fresh token is expected to deliver the backlog.
func TestARejectedTokenHoldsTheListen(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.api.setReply(func(submitCall) (int, string) {
		return http.StatusUnauthorized, `{"code":401,"error":"invalid token"}`
	})

	h.stream(0)
	h.listenThrough(0, 100)

	if h.queueSize() != 1 {
		t.Fatalf("queue = %d after a rejected token, want 1 (the listen must be kept)", h.queueSize())
	}
}

// Reconnecting clears the backoff, so a fresh token delivers the backlog at
// once instead of after a wait.
func TestReconnectingFlushesTheHeldBacklog(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.api.setReply(func(submitCall) (int, string) {
		return http.StatusUnauthorized, `{"code":401,"error":"invalid token"}`
	})

	h.stream(0)
	h.listenThrough(0, 100)
	if h.queueSize() != 1 {
		t.Fatalf("queue = %d, want 1", h.queueSize())
	}

	h.api.setReply(nil)
	h.connect() // paste a fresh token

	delivered, err := h.service.FlushQueue(context.Background(), users.BootstrapUserID, 50)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("flushed %d immediately after reconnecting, want 1", delivered)
	}
}

// ---------------------------------------------------------------------------
// batching
// ---------------------------------------------------------------------------

// A backlog goes out in one request rather than one request per listen, which
// is the advantage ListenBrainz's API has over Last.fm's.
func TestABacklogIsDeliveredAsOneBatch(t *testing.T) {
	h := newHarness(t)
	h.connect()

	queueListens(t, h, 5)

	h.api.reset()
	h.clock.Advance(2 * time.Hour)
	delivered, err := h.service.FlushQueue(context.Background(), users.BootstrapUserID, 50)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if delivered != 5 {
		t.Fatalf("delivered %d, want 5", delivered)
	}
	imports := h.api.of(listenTypeImport)
	if len(imports) != 1 {
		t.Fatalf("import submissions = %d, want 1 batch", len(imports))
	}
	if len(imports[0].Listens) != 5 {
		t.Fatalf("batch carried %d listens, want 5", len(imports[0].Listens))
	}
	// Oldest first keeps a recovered history coherent.
	for i := 1; i < len(imports[0].Listens); i++ {
		if imports[0].Listens[i-1].ListenedAt > imports[0].Listens[i].ListenedAt {
			t.Fatal("batch is not in chronological order")
		}
	}
}

// One unacceptable listen must not hold the rest of the batch hostage.
func TestOneBadListenInABatchDoesNotBlockTheOthers(t *testing.T) {
	h := newHarness(t)
	h.connect()

	queueListens(t, h, 4)

	h.api.reset()
	h.clock.Advance(2 * time.Hour)

	// The batch is rejected; individually, only the third listen is.
	poison := ""
	h.api.setReply(func(call submitCall) (int, string) {
		if call.ListenType == listenTypeImport {
			return http.StatusBadRequest, `{"code":400,"error":"one of these is bad"}`
		}
		if len(call.Listens) == 1 && call.Listens[0].TrackMetadata.TrackName == "Signal One #3" {
			poison = call.Listens[0].TrackMetadata.TrackName
			return http.StatusBadRequest, `{"code":400,"error":"bad listen"}`
		}
		return http.StatusOK, `{"status":"ok"}`
	})

	delivered, err := h.service.FlushQueue(context.Background(), users.BootstrapUserID, 50)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if delivered != 3 {
		t.Fatalf("delivered %d, want 3 (every listen but the bad one)", delivered)
	}
	if poison == "" {
		t.Fatal("the bad listen was never retried individually")
	}
	if h.queueSize() != 0 {
		t.Fatalf("queue = %d, want 0 (the bad listen dropped, the rest delivered)", h.queueSize())
	}
}

// queueListens strands n distinct listens in the queue by failing delivery.
func queueListens(t *testing.T, h *harness, n int) {
	t.Helper()
	h.api.setReply(func(submitCall) (int, string) {
		return http.StatusInternalServerError, `{"code":500,"error":"down"}`
	})
	for i := 1; i <= n; i++ {
		track := trackOf(161)
		track.ID = fmt.Sprintf("track-%d", i)
		track.Title = fmt.Sprintf("Signal One #%d", i)
		h.track = track
		h.clock.Advance(time.Minute)
		h.stream(0)
		h.listenThrough(0, 100)
	}
	if got := h.queueSize(); got != n {
		t.Fatalf("queued %d listens, want %d", got, n)
	}
	h.api.setReply(nil)
}

// ---------------------------------------------------------------------------
// history
// ---------------------------------------------------------------------------

func TestHistoryRecordsWhatHappened(t *testing.T) {
	h := newHarness(t)
	h.connect()

	h.stream(0)
	h.listenThrough(0, 100)

	page, err := h.service.ListHistory(context.Background(), users.BootstrapUserID, 50, 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if page.Total == 0 {
		t.Fatal("history is empty after a listen")
	}
	found := false
	for _, item := range page.Items {
		if item.Kind == queueKindListen && item.Status == submissionStatusSubmitted {
			found = true
			if item.Track != "Signal One" {
				t.Fatalf("history track = %q", item.Track)
			}
			if item.CreatedAt.IsZero() {
				t.Fatal("history row has no created_at")
			}
		}
	}
	if !found {
		t.Fatal("no submitted listen in history")
	}
}

func TestQueuePageReportsHeldListens(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.api.setReply(func(submitCall) (int, string) {
		return http.StatusInternalServerError, `{"code":500,"error":"down"}`
	})

	h.stream(0)
	h.listenThrough(0, 100)

	page, err := h.service.ListQueue(context.Background(), users.BootstrapUserID, 50, 0)
	if err != nil {
		t.Fatalf("queue page: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("queue page = %+v, want one held listen", page)
	}
	item := page.Items[0]
	if item.Track != "Signal One" || item.Attempts < 1 {
		t.Fatalf("queue item = %+v", item)
	}
	if item.NextAttemptAt == nil {
		t.Fatal("a held listen must carry its next attempt time")
	}
	if item.LastError == "" {
		t.Fatal("a held listen must record why it failed")
	}
	if item.CreatedAt.IsZero() {
		t.Fatal("queue item has no created_at")
	}
}

// ---------------------------------------------------------------------------
// isolation
// ---------------------------------------------------------------------------

// Nothing is measured or submitted for a user who never connected.
func TestAnUnconnectedUserIsIgnored(t *testing.T) {
	h := newHarness(t)

	h.stream(0)
	h.listenThrough(0, 100)

	if got := h.api.listensSubmitted(); got != 0 {
		t.Fatalf("listens submitted = %d for an unconnected user, want 0", got)
	}
	if h.queueSize() != 0 {
		t.Fatalf("queue = %d for an unconnected user, want 0", h.queueSize())
	}
}

// Disconnecting keeps the backlog: the usual reason to disconnect is to paste
// a fresh token, and dropping earned listens would be a data loss.
func TestDisconnectKeepsQueuedListens(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.api.setReply(func(submitCall) (int, string) {
		return http.StatusInternalServerError, `{"code":500,"error":"down"}`
	})

	h.stream(0)
	h.listenThrough(0, 100)
	if h.queueSize() != 1 {
		t.Fatalf("queue = %d, want 1", h.queueSize())
	}

	if err := h.service.Disconnect(context.Background(), users.BootstrapUserID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if h.queueSize() != 1 {
		t.Fatalf("queue = %d after disconnecting, want the listen kept", h.queueSize())
	}
	status, _ := h.service.Status(context.Background(), users.BootstrapUserID)
	if status.Connected {
		t.Fatal("still connected after disconnect")
	}
}
