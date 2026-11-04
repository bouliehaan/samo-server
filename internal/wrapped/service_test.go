package wrapped

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/scrobble"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

var epoch = time.Date(2026, 8, 14, 21, 5, 0, 0, time.UTC)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

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

// fakeLastFM is a linked (or not) Last.fm account with a fixed history.
type fakeLastFM struct {
	mu       sync.Mutex
	linked   map[string]string
	listens  []lastfm.RecentListen
	reads    int
	release  chan struct{} // when set, a read waits for it
	readDone chan struct{}
}

func (f *fakeLastFM) LinkedUsername(_ context.Context, userID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name, ok := f.linked[userID]
	return name, ok
}

func (f *fakeLastFM) UserListens(_ context.Context, _ string, _, _ time.Time, progress func(page, pages int)) ([]lastfm.RecentListen, error) {
	f.mu.Lock()
	f.reads++
	release := f.release
	f.mu.Unlock()
	if progress != nil {
		progress(1, 2)
	}
	if release != nil {
		<-release
	}
	defer func() {
		if f.readDone != nil {
			f.readDone <- struct{}{}
		}
	}()
	return f.listens, nil
}

type harness struct {
	t       *testing.T
	db      *sql.DB
	clock   *clock
	lastfm  *fakeLastFM
	service *Service
	track   catalog.MusicTrack
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := storagetest.Open(t)
	for _, user := range []string{"jake", "sam"} {
		if _, err := db.Exec(`INSERT INTO users (id, username, role) VALUES (?, ?, 'user')`, user, user); err != nil {
			t.Fatalf("user: %v", err)
		}
	}
	h := &harness{
		t:      t,
		db:     db,
		clock:  &clock{now: epoch},
		lastfm: &fakeLastFM{linked: map[string]string{}},
		track: catalog.MusicTrack{
			ID:              "track-1",
			Title:           "Night Drive",
			ArtistNames:     []string{"Aurora Fields"},
			AlbumTitle:      "Night Drive",
			DurationSeconds: 200,
		},
	}
	h.service = NewService(ServiceOptions{
		DB:     db,
		LastFM: h.lastfm,
		Zone:   time.UTC,
		Now:    h.clock.Now,
		Logger: func(string, ...any) {},
	})
	return h
}

// listen plays the whole track as a client does: the stream opens, then
// position reports every 20 seconds.
func (h *harness) listen(user string) {
	h.t.Helper()
	ctx := context.Background()
	h.service.HandlePlayback(ctx, scrobble.PlaybackInput{
		UserID: user, Track: h.track, Source: scrobble.SourceStream, ObservedAt: h.clock.Now(),
	})
	for position := 20; position <= h.track.DurationSeconds; position += 20 {
		h.clock.Advance(20 * time.Second)
		h.service.HandlePlayback(ctx, scrobble.PlaybackInput{
			UserID: user, Track: h.track, Source: "playback-patch",
			Before:     catalog.PlaybackState{ProgressSeconds: position - 20},
			After:      catalog.PlaybackState{ProgressSeconds: position},
			ObservedAt: h.clock.Now(),
		})
	}
	h.clock.Advance(2 * time.Minute)
}

func (h *harness) logged(user string) int {
	h.t.Helper()
	settings, err := h.service.Settings(context.Background(), user)
	if err != nil {
		h.t.Fatal(err)
	}
	return settings.Listens
}

func TestNothingIsLoggedUntilTheListenerTurnsHistoryOn(t *testing.T) {
	h := newHarness(t)
	h.listen("sam")
	if got := h.logged("sam"); got != 0 {
		t.Fatalf("logged %d listens with history off", got)
	}
	var plays int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM wrapped_plays`).Scan(&plays); err != nil || plays != 0 {
		t.Fatalf("kept %d play rows with history off (%v)", plays, err)
	}
	if _, _, err := h.service.Report(context.Background(), "sam", 2026, false); !IsNoSource(err) {
		t.Fatalf("report error = %v, want no source", err)
	}
}

func TestEachEarnedListenIsLoggedOnceAndOnlyForThatListener(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.service.SetLocalHistory(ctx, "sam", true); err != nil {
		t.Fatal(err)
	}
	h.listen("sam")
	h.listen("sam")
	h.listen("jake") // history off for jake
	if got := h.logged("sam"); got != 2 {
		t.Fatalf("sam has %d listens, want 2", got)
	}
	if got := h.logged("jake"); got != 0 {
		t.Fatalf("jake has %d listens, want 0", got)
	}

	// A play too short to count is not a listen.
	h.service.HandlePlayback(ctx, scrobble.PlaybackInput{UserID: "sam", Track: h.track, Source: scrobble.SourceStream, ObservedAt: h.clock.Now()})
	h.clock.Advance(20 * time.Second)
	h.service.HandlePlayback(ctx, scrobble.PlaybackInput{UserID: "sam", Track: h.track, Source: "playback-patch",
		After: catalog.PlaybackState{ProgressSeconds: 20}, ObservedAt: h.clock.Now()})
	if got := h.logged("sam"); got != 2 {
		t.Fatalf("a 20 second play was logged: %d", got)
	}

	report, building, err := h.service.Report(ctx, "sam", 2026, false)
	if err != nil || building != nil {
		t.Fatalf("report: %v building %v", err, building)
	}
	if report.Source != SourceSamo || report.Music == nil || report.Music.Listens != 2 {
		t.Fatalf("report = %+v", report)
	}
	if got := report.Music.TopTracks[0]; got.Name != "Night Drive" || got.Plays != 2 {
		t.Fatalf("top track = %+v", got)
	}
	// It covers only the time since history was turned on.
	if !report.From.Equal(epoch) {
		t.Fatalf("report from %v, want %v", report.From, epoch)
	}
	if _, _, err := h.service.Report(ctx, "jake", 2026, false); !IsNoSource(err) {
		t.Fatalf("jake's report error = %v, want no source", err)
	}
}

func TestTurningHistoryOffStopsLoggingAndDeletingErasesIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.service.SetLocalHistory(ctx, "sam", true)
	h.listen("sam")
	settings, _ := h.service.SetLocalHistory(ctx, "sam", false)
	if settings.LocalHistory || settings.Listens != 1 {
		t.Fatalf("off keeps what was logged: %+v", settings)
	}
	h.listen("sam")
	if got := h.logged("sam"); got != 1 {
		t.Fatalf("logged with history off: %d", got)
	}
	settings, err := h.service.DeleteHistory(ctx, "sam")
	if err != nil || settings.Listens != 0 || settings.EnabledAt != nil {
		t.Fatalf("after delete: %+v %v", settings, err)
	}
}

func TestALastFMYearIsReadOnceInTheBackgroundThenKept(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.lastfm.linked["jake"] = "jake_fm"
	h.lastfm.release = make(chan struct{})
	h.lastfm.readDone = make(chan struct{}, 1)
	h.lastfm.listens = []lastfm.RecentListen{
		{Artist: "Delta Static", Track: "Frequencies", Album: "Frequencies", At: time.Date(2026, 3, 1, 22, 0, 0, 0, time.UTC)},
		{Artist: "Delta Static", Track: "Frequencies", Album: "Frequencies", At: time.Date(2026, 3, 2, 22, 0, 0, 0, time.UTC)},
	}

	report, building, err := h.service.Report(ctx, "jake", 2026, false)
	if err != nil || report != nil || building == nil {
		t.Fatalf("first ask: report %v building %v err %v", report, building, err)
	}
	// Asking again while it reads does not start a second read.
	if _, again, _ := h.service.Report(ctx, "jake", 2026, false); again == nil || again.Pages != 1 || again.TotalPages != 2 {
		t.Fatalf("progress = %+v", again)
	}
	close(h.lastfm.release)
	<-h.lastfm.readDone
	// The report is saved just after the read returns.
	deadline := time.Now().Add(5 * time.Second)
	for {
		report, building, err = h.service.Report(ctx, "jake", 2026, false)
		if report != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || report == nil {
		t.Fatalf("built: %v %v", building, err)
	}
	if report.Source != SourceLastFM || report.LastFMUser != "jake_fm" || report.Music.Listens != 2 {
		t.Fatalf("report = %+v", report)
	}
	// Scrobbles from outside the library are estimated.
	if !report.Music.MinutesEstimated || report.Music.Minutes != 2*estimatedTrackSeconds/60 {
		t.Fatalf("minutes %d estimated %v", report.Music.Minutes, report.Music.MinutesEstimated)
	}
	h.service.Report(ctx, "jake", 2026, false)
	if h.lastfm.reads != 1 {
		t.Fatalf("Last.fm was read %d times, want 1", h.lastfm.reads)
	}
	// Last.fm wins over samo's own log: it holds everything scrobbled.
	h.service.SetLocalHistory(ctx, "jake", true)
	if again, _, _ := h.service.Report(ctx, "jake", 2026, false); again.Source != SourceLastFM {
		t.Fatalf("source = %s", again.Source)
	}
}

func TestAYearNotStartedIsRefused(t *testing.T) {
	h := newHarness(t)
	if _, _, err := h.service.Report(context.Background(), "sam", 2027, false); err != ErrBadYear {
		t.Fatalf("err = %v", err)
	}
}
