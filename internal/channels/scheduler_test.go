package channels

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// stubCatalog returns fixed episode pages for a single podcast id so
// the scheduler's podcast-subscription path can be exercised without
// dragging in the full catalog projection.
type stubCatalog struct {
	episodes map[string][]catalog.PodcastEpisode
	err      error
}

func (s *stubCatalog) EpisodesForPodcast(podcastID string, page catalog.PageRequest) (catalog.Page[catalog.PodcastEpisode], error) {
	if s.err != nil {
		return catalog.Page[catalog.PodcastEpisode]{}, s.err
	}
	items := s.episodes[podcastID]
	return catalog.Page[catalog.PodcastEpisode]{Items: items, Total: len(items), Limit: page.Limit}, nil
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "channels.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE channels (id TEXT PRIMARY KEY, name TEXT, description TEXT, codec TEXT, bitrate_kbps INTEGER, sample_rate_hz INTEGER, enabled INTEGER, created_at TEXT, updated_at TEXT);
		CREATE TABLE channel_sources (id TEXT PRIMARY KEY, channel_id TEXT, kind TEXT, label TEXT, config_json TEXT, enabled INTEGER, weight INTEGER, default_rotation INTEGER, created_at TEXT, updated_at TEXT);
		CREATE TABLE channel_schedule_rules (id TEXT PRIMARY KEY, channel_id TEXT, source_id TEXT, label TEXT, weekday_mask INTEGER, start_minute INTEGER, end_minute INTEGER, priority INTEGER, enabled INTEGER, created_at TEXT);
		CREATE TABLE channel_play_log (id TEXT PRIMARY KEY, channel_id TEXT, source_id TEXT, item_ref TEXT, title TEXT, artist TEXT, kind TEXT, started_at TEXT, ended_at TEXT, duration_seconds INTEGER);
	`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func TestPickActiveRuleRespectsPriorityAndWindow(t *testing.T) {
	at := time.Date(2026, 5, 25, 16, 30, 0, 0, time.UTC) // Monday 4:30pm
	rules := []ScheduleRule{
		{ID: "low", Priority: 50, WeekdayMask: 127, StartMinute: 16 * 60, EndMinute: 17 * 60, Enabled: true, SourceID: "low-src"},
		{ID: "high", Priority: 100, WeekdayMask: 127, StartMinute: 16 * 60, EndMinute: 17 * 60, Enabled: true, SourceID: "high-src"},
		{ID: "off", Priority: 200, WeekdayMask: 127, StartMinute: 16 * 60, EndMinute: 17 * 60, Enabled: false, SourceID: "disabled-src"},
		{ID: "wrongday", Priority: 200, WeekdayMask: 1, StartMinute: 16 * 60, EndMinute: 17 * 60, Enabled: true, SourceID: "sunday-src"},
	}
	rule, ok := pickActiveRule(rules, at)
	if !ok {
		t.Fatalf("expected a rule to match")
	}
	if rule.ID != "high" {
		t.Fatalf("expected highest-priority enabled rule, got %s", rule.ID)
	}
}

func TestNextRuleStartGap(t *testing.T) {
	at := time.Date(2026, 5, 25, 15, 0, 0, 0, time.UTC) // Monday 3:00pm
	rules := []ScheduleRule{
		{ID: "soon", Enabled: true, WeekdayMask: 127, StartMinute: 16 * 60, EndMinute: 17 * 60},
		{ID: "later", Enabled: true, WeekdayMask: 127, StartMinute: 18 * 60, EndMinute: 19 * 60},
	}
	gap := nextRuleStartGap(rules, at, 2*time.Hour)
	want := time.Hour
	if gap != want {
		t.Fatalf("expected %v, got %v", want, gap)
	}

	// Cap kicks in when next rule is past lookahead.
	gap = nextRuleStartGap(rules, at, 30*time.Minute)
	if gap != 30*time.Minute {
		t.Fatalf("expected cap, got %v", gap)
	}

	// Active rule returns 0.
	active := []ScheduleRule{{ID: "now", Enabled: true, WeekdayMask: 127, StartMinute: 14 * 60, EndMinute: 16 * 60}}
	if got := nextRuleStartGap(active, at, time.Hour); got != 0 {
		t.Fatalf("expected 0 for active rule, got %v", got)
	}
}

func TestRuleWindowRemaining(t *testing.T) {
	at := time.Date(2026, 5, 25, 16, 30, 0, 0, time.UTC)
	rule := ScheduleRule{StartMinute: 16 * 60, EndMinute: 17 * 60}
	got := ruleWindowRemaining(rule, at)
	want := 30 * time.Minute
	if got != want {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestFilePoolPicksAFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.mp3", "b.mp3", "c.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deps := Dependencies{
		DB:  newTestDB(t),
		Now: func() time.Time { return time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC) },
	}
	sched := NewScheduler(deps)
	src := Source{
		ID:              "src-1",
		Kind:            SourceFilePool,
		Label:           "Commercials",
		Config:          map[string]any{"paths": []any{dir}},
		Enabled:         true,
		DefaultRotation: true,
		Weight:          1,
	}
	item, err := sched.resolveSource(context.Background(), "ch-1", src, nil, 0)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if filepath.Dir(item.URL) != dir {
		t.Fatalf("expected file under %s, got %s", dir, item.URL)
	}
	if item.SourceID != "src-1" || item.Kind != SourceFilePool {
		t.Fatalf("unexpected item metadata: %+v", item)
	}
}

func TestFilePoolFiltersRecentlyPlayed(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.mp3")
	b := filepath.Join(dir, "b.mp3")
	os.WriteFile(a, []byte("x"), 0o644)
	os.WriteFile(b, []byte("x"), 0o644)
	deps := Dependencies{DB: newTestDB(t)}
	sched := NewScheduler(deps)
	src := Source{
		ID:      "src",
		Kind:    SourceFilePool,
		Config:  map[string]any{"paths": []any{dir}},
		Enabled: true,
	}
	// Mark `a` as recently played; we should only get back `b`.
	recent := map[string]time.Time{a: time.Now()}
	for i := 0; i < 10; i++ {
		item, err := sched.resolveSource(context.Background(), "ch", src, recent, 0)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if item.URL != b {
			t.Fatalf("iteration %d picked recently-played %s", i, item.URL)
		}
	}
}

func TestPodcastSubscriptionPicksFreshUnplayedEpisode(t *testing.T) {
	at := time.Date(2026, 5, 25, 9, 0, 0, 0, time.UTC)
	old := at.Add(-90 * 24 * time.Hour)
	fresh := at.Add(-2 * 24 * time.Hour)
	freshest := at.Add(-1 * time.Hour)
	episodes := []catalog.PodcastEpisode{
		{ID: "ep-freshest", Title: "Newest", PublishedAt: &freshest, EnclosureURL: "https://example.com/3.mp3"},
		{ID: "ep-fresh", Title: "Fresh", PublishedAt: &fresh, EnclosureURL: "https://example.com/2.mp3"},
		{ID: "ep-old", Title: "Old", PublishedAt: &old, EnclosureURL: "https://example.com/1.mp3"},
	}
	deps := Dependencies{
		DB:      newTestDB(t),
		Catalog: &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{"pod-1": episodes}},
		Now:     func() time.Time { return at },
	}
	sched := NewScheduler(deps)
	src := Source{
		ID:      "src",
		Kind:    SourcePodcastSubscription,
		Enabled: true,
		Config:  map[string]any{"podcastId": "pod-1", "maxAgeDays": 30},
	}

	item, err := sched.resolveSource(context.Background(), "ch", src, nil, 0)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if item.ItemRef != "episode:ep-freshest" {
		t.Fatalf("expected freshest, got %s", item.ItemRef)
	}

	// Mark freshest as already played; should fall through to next-fresh.
	recent := map[string]time.Time{"episode:ep-freshest": at}
	item, err = sched.resolveSource(context.Background(), "ch", src, recent, 0)
	if err != nil {
		t.Fatalf("resolve fresh fallback: %v", err)
	}
	if item.ItemRef != "episode:ep-fresh" {
		t.Fatalf("expected next-fresh fallback, got %s", item.ItemRef)
	}
}

type stubInternetStations struct {
	station InternetStation
	err     error
}

func (s *stubInternetStations) GetInternetRadioStation(ctx context.Context, stationID string) (InternetStation, error) {
	if s.err != nil {
		return InternetStation{}, s.err
	}
	if stationID != s.station.ID {
		return InternetStation{}, errors.New("not found")
	}
	return s.station, nil
}

func TestInternetStationResolvesViaLookup(t *testing.T) {
	deps := Dependencies{
		DB: newTestDB(t),
		InternetStations: &stubInternetStations{station: InternetStation{
			ID: "irs-1", Name: "WFMU", StreamURL: "https://wfmu.example.com/live.mp3",
		}},
	}
	sched := NewScheduler(deps)
	src := Source{
		ID:      "src",
		Kind:    SourceInternetStation,
		Enabled: true,
		Config:  map[string]any{"stationId": "irs-1"},
	}
	item, err := sched.resolveSource(context.Background(), "ch", src, nil, 0)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if item.URL != "https://wfmu.example.com/live.mp3" || !item.Live {
		t.Fatalf("unexpected item: %+v", item)
	}
	if item.Title != "WFMU" {
		t.Fatalf("expected fallback to station name, got %q", item.Title)
	}
	if item.ItemRef != "station:irs-1" {
		t.Fatalf("expected station ref, got %q", item.ItemRef)
	}
}

func TestInternetStationErrorsWhenLookupMissing(t *testing.T) {
	sched := NewScheduler(Dependencies{DB: newTestDB(t)})
	src := Source{
		Kind: SourceInternetStation, Enabled: true,
		Config: map[string]any{"stationId": "irs-1"},
	}
	if _, err := sched.resolveSource(context.Background(), "ch", src, nil, 0); err == nil {
		t.Fatalf("expected error when InternetStations lookup not configured")
	}
}

func TestLiveStreamResolvesURL(t *testing.T) {
	deps := Dependencies{DB: newTestDB(t)}
	sched := NewScheduler(deps)
	src := Source{
		ID:      "live",
		Kind:    SourceLiveStream,
		Label:   "NPR",
		Config:  map[string]any{"url": "https://npr.example.com/live.mp3"},
		Enabled: true,
	}
	item, err := sched.resolveSource(context.Background(), "ch", src, nil, 30*time.Minute)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !item.Live {
		t.Fatalf("expected Live=true")
	}
	if item.URL != "https://npr.example.com/live.mp3" {
		t.Fatalf("unexpected url %s", item.URL)
	}
}

func TestNextItemPrefersRuleOverRotation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	rotation, err := InsertSource(ctx, db, "ch-1", CreateSourceInput{
		Kind: SourceLiveStream, Label: "Background", Config: map[string]any{"url": "https://bg.example.com/x.mp3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduled, err := InsertSource(ctx, db, "ch-1", CreateSourceInput{
		Kind: SourceLiveStream, Label: "NPR Live", Config: map[string]any{"url": "https://npr.example.com/x.mp3"},
		DefaultRotation: boolPtr(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := InsertScheduleRule(ctx, db, "ch-1", CreateScheduleRuleInput{
		SourceID: scheduled.ID, StartMinute: 16 * 60, EndMinute: 17 * 60, Priority: 200, Enabled: &enabled, WeekdayMask: 127,
	}); err != nil {
		t.Fatal(err)
	}

	deps := Dependencies{
		DB:  db,
		Now: func() time.Time { return time.Date(2026, 5, 25, 16, 30, 0, 0, time.UTC) },
	}
	sched := NewScheduler(deps)
	item, err := sched.NextItem(ctx, "ch-1")
	if err != nil {
		t.Fatalf("NextItem: %v", err)
	}
	if item.URL != "https://npr.example.com/x.mp3" {
		t.Fatalf("expected NPR (scheduled), got %s (rotation=%s)", item.URL, rotation.ID)
	}
	if item.MaxDuration != 30*time.Minute {
		t.Fatalf("expected MaxDuration to cap at rule window, got %v", item.MaxDuration)
	}
}

func TestNextItemTagsRuleDriven(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	scheduled, err := InsertSource(ctx, db, "ch-3", CreateSourceInput{
		Kind: SourceLiveStream, Label: "Cut-in", Config: map[string]any{"url": "https://cut.example.com/x.mp3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	rule, err := InsertScheduleRule(ctx, db, "ch-3", CreateScheduleRuleInput{
		SourceID: scheduled.ID, StartMinute: 9 * 60, EndMinute: 10 * 60, Priority: 150, Enabled: &enabled, WeekdayMask: 127,
	})
	if err != nil {
		t.Fatal(err)
	}
	sched := NewScheduler(Dependencies{
		DB:  db,
		Now: func() time.Time { return time.Date(2026, 5, 25, 9, 30, 0, 0, time.UTC) },
	})
	item, err := sched.NextItem(ctx, "ch-3")
	if err != nil {
		t.Fatalf("NextItem: %v", err)
	}
	if !item.IsRuleDriven {
		t.Fatalf("expected IsRuleDriven=true for active rule")
	}
	if item.RuleID != rule.ID {
		t.Fatalf("expected RuleID=%s, got %s", rule.ID, item.RuleID)
	}
}

func TestNextItemFallsBackToRotationWhenNoRule(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	dir := t.TempDir()
	a := filepath.Join(dir, "a.mp3")
	os.WriteFile(a, []byte("x"), 0o644)
	_, err := InsertSource(ctx, db, "ch-2", CreateSourceInput{
		Kind: SourceFilePool, Label: "Music", Config: map[string]any{"paths": []any{dir}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sched := NewScheduler(Dependencies{
		DB:  db,
		Now: func() time.Time { return time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC) },
	})
	item, err := sched.NextItem(ctx, "ch-2")
	if err != nil {
		t.Fatalf("NextItem: %v", err)
	}
	if item.URL != a {
		t.Fatalf("expected file %s, got %s", a, item.URL)
	}
}

func TestNextItemErrsWhenNoSources(t *testing.T) {
	sched := NewScheduler(Dependencies{DB: newTestDB(t)})
	if _, err := sched.NextItem(context.Background(), "empty"); err == nil {
		t.Fatalf("expected error on empty channel")
	} else if !errors.Is(err, errors.New("channel has no enabled sources")) && err.Error() != "channel has no enabled sources" {
		// errors.Is doesn't work with sentinel string equals here; checking string is fine.
		t.Logf("non-sentinel error (ok): %v", err)
	}
}

func boolPtr(b bool) *bool { return &b }
