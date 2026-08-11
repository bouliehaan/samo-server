package channels

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// The database-backed half: a real schema, real rows, and the scheduler shell
// that fetches a plan, decides, and writes back what it did. The decision logic
// itself is tested without a database in engine_test.go.

// stubCatalog returns fixed episode pages for a single podcast id so the
// podcast path can be exercised without dragging in the full catalog
// projection.
type stubCatalog struct {
	episodes  map[string][]catalog.PodcastEpisode
	playlists map[string][]catalog.MusicTrack
	err       error
}

func (s *stubCatalog) MusicTracksForPlaylist(playlistID string) []catalog.MusicTrack {
	return s.playlists[playlistID]
}

func (s *stubCatalog) EpisodesForPodcast(podcastID string, page catalog.PageRequest) (catalog.Page[catalog.PodcastEpisode], error) {
	if s.err != nil {
		return catalog.Page[catalog.PodcastEpisode]{}, s.err
	}
	items := s.episodes[podcastID]
	return catalog.Page[catalog.PodcastEpisode]{Items: items, Total: len(items), Limit: page.Limit}, nil
}

type stubInternetStations struct {
	station InternetStation
	err     error
}

func (s *stubInternetStations) GetInternetRadioStation(_ context.Context, stationID string) (InternetStation, error) {
	if s.err != nil {
		return InternetStation{}, s.err
	}
	if s.station.ID != stationID {
		return InternetStation{}, errors.New("no such station")
	}
	return s.station, nil
}

// newTestDB hands back a real migrated database, so queries run against the
// actual channels schema instead of a hand-rolled shadow copy.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return storagetest.Open(t)
}

// mustChannel seeds the parent channels row the real schema's foreign keys
// require before sources/rules can reference it.
func mustChannel(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO channels (id, name) VALUES (?, ?)`, id, "Channel "+id); err != nil {
		t.Fatalf("seed channel %s: %v", id, err)
	}
}

func boolPtr(b bool) *bool { return &b }

func mustSource(t *testing.T, db *sql.DB, channelID string, input CreateSourceInput) Source {
	t.Helper()
	src, err := InsertSource(context.Background(), db, channelID, input)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	return src
}

// ---- resolving one item at a time --------------------------------------

func TestFilePoolPlaysAFileFromTheFolder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.mp3", "two.mp3", ".hidden.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	src := Source{
		ID: "files", Kind: SourceFilePool, Label: "Idents", Enabled: true, Role: RoleMusic,
		Config: map[string]any{"paths": []string{dir}},
	}
	engine := &Engine{Sources: []Source{src}, Location: time.UTC}
	env := enumerationContext{now: time.Now(), searchDepth: 50, day: DefaultListeningDay}

	candidates := engine.enumerateSource(context.Background(), src, env)
	if len(candidates) != 2 {
		t.Fatalf("expected two files (hidden ones skipped), got %d", len(candidates))
	}
	item, err := engine.Materialise(context.Background(), candidates[0])
	if err != nil {
		t.Fatalf("materialise: %v", err)
	}
	if filepath.Dir(item.URL) != dir {
		t.Fatalf("item URL %q is not in the pool directory", item.URL)
	}
}

func TestPodcastEnumerationOrdersNewestFirst(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	src := podcastSource("pod1", "Show", "p1")
	engine := &Engine{
		Sources:     []Source{src},
		Location:    time.UTC,
		Obligations: NewMemoryObligations(),
		Catalog: &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{"p1": {
			// Feed order: oldest first, which is what an ingested feed gives us.
			episode("old", "Old", now.Add(-400*24*time.Hour), 30),
			episode("new", "New", now.Add(-2*time.Hour), 30),
		}}},
	}
	env := enumerationContext{now: now, searchDepth: 50, day: DefaultListeningDay, heardInDay: map[string]int{}}
	env.owed = engine.refreshObligations(context.Background(), now, env)

	candidates := engine.enumerateSource(context.Background(), src, env)
	if len(candidates) != 2 {
		t.Fatalf("expected two episodes, got %d", len(candidates))
	}
	if candidates[0].Ref != "episode:new" {
		t.Fatalf("newest should come first, got %q", candidates[0].Ref)
	}
	if !candidates[0].Owed {
		t.Fatalf("an episode published two hours ago, inside the listening day, is owed")
	}
	if candidates[1].Owed {
		t.Fatalf("a year-old episode is back catalogue")
	}
}

func TestInternetStationResolvesViaLookup(t *testing.T) {
	engine := &Engine{
		Location: time.UTC,
		Stations: &stubInternetStations{station: InternetStation{
			ID: "st1", Name: "Public Radio", StreamURL: "http://example.test/live",
		}},
	}
	src := Source{
		ID: "s1", Kind: SourceInternetStation, Enabled: true, Role: RoleTalk,
		Config: map[string]any{"stationId": "st1"},
	}
	item, err := engine.resolveInternetStation(context.Background(), src)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if item.URL != "http://example.test/live" || !item.Live {
		t.Fatalf("unexpected item %+v", item)
	}
	if item.ItemRef != "station:st1" {
		t.Fatalf("item ref %q should namespace the station id", item.ItemRef)
	}
}

func TestInternetStationErrorsWhenLookupMissing(t *testing.T) {
	engine := &Engine{Location: time.UTC}
	src := Source{ID: "s1", Kind: SourceInternetStation, Config: map[string]any{"stationId": "st1"}}
	if _, err := engine.resolveInternetStation(context.Background(), src); err == nil {
		t.Fatalf("expected an error when no station lookup is configured")
	}
}

func TestLiveStreamResolvesURL(t *testing.T) {
	engine := &Engine{Location: time.UTC}
	src := Source{
		ID: "s1", Kind: SourceLiveStream, Enabled: true, Role: RoleTalk,
		Config: map[string]any{"url": "https://example.test/stream.mp3"},
	}
	item, err := engine.resolveLiveStream(src)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !item.Live || item.URL != "https://example.test/stream.mp3" {
		t.Fatalf("unexpected item %+v", item)
	}
	bad := Source{ID: "s2", Kind: SourceLiveStream, Config: map[string]any{"url": "not a url"}}
	if _, err := engine.resolveLiveStream(bad); err == nil {
		t.Fatalf("expected an error for a URL with no scheme")
	}
}

// A continuous source has no length of its own, so the ceiling has to be
// applied to how long the station stays on it.
func TestContinuousItemsGetAPlayWindow(t *testing.T) {
	engine := &Engine{Location: time.UTC}
	candidate := Candidate{
		Traits: Traits{Continuous: true},
		source: Source{ID: "s1", Kind: SourceLiveStream, Config: map[string]any{"playMinutes": 90}},
	}
	item := PlaybackItem{}
	engine.applyDuration(&item, candidate, ProgrammingIntent{
		Window: 30 * time.Minute, PlayCeiling: 30 * time.Minute,
	}, Timeline{}, BlockDecision{})
	if item.MaxDuration != 30*time.Minute {
		t.Fatalf("the window before the next slot should win: got %s", item.MaxDuration)
	}

	item = PlaybackItem{}
	engine.applyDuration(&item, candidate, ProgrammingIntent{}, Timeline{}, BlockDecision{})
	if item.MaxDuration != 90*time.Minute {
		t.Fatalf("with nothing booked ahead the source's own window applies: got %s", item.MaxDuration)
	}
}

// ---- the shell: plan, decide, write back -------------------------------

func TestNextItemUsesTheDerivedPlanAndRecordsState(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustChannel(t, db, "ch1")
	mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourcePodcastSubscription, Label: "Show", Role: RoleTalk,
		Config: map[string]any{"podcastId": "p1"}, Enabled: boolPtr(true),
	})

	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	sched := NewScheduler(Dependencies{
		DB:  db,
		Now: func() time.Time { return now },
		Catalog: &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
			"p1": {episode("e1", "An episode", now.Add(-3*time.Hour), 30)},
		}},
		Skips: NewSkipRegistry(func() time.Time { return now }),
	})

	item, err := sched.NextItem(ctx, "ch1")
	if err != nil {
		t.Fatalf("next item: %v", err)
	}
	if item.ItemRef != "episode:e1" {
		t.Fatalf("expected the episode, got %q", item.ItemRef)
	}
	if item.BlockID == "" {
		t.Fatalf("the item should record which block chose it")
	}

	state, err := LoadProgramState(ctx, db, "ch1")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.BlockID != item.BlockID {
		t.Fatalf("programme state %q does not match the item's block %q", state.BlockID, item.BlockID)
	}

	// The whole state has to round-trip, not just the three columns it started
	// with. The break flag in particular only matters BETWEEN decisions: without
	// it, the rule that puts a break between two things re-fires on the break's
	// own last item and the station plays nothing else. That failure is
	// invisible until the state goes through the database.
	full := ProgramState{
		BlockID: "general", EnteredAt: now, ItemCount: 4, PatternIndex: 3,
		LastWasBreak: true,
		Queue:        []QueuedItem{{SourceID: "s1", Ref: "track:x", Position: 2, Of: 3, Reason: "stopset"}},
	}
	if err := SaveProgramState(ctx, db, "ch1", full); err != nil {
		t.Fatalf("save state: %v", err)
	}
	reloaded, err := LoadProgramState(ctx, db, "ch1")
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if !reloaded.LastWasBreak {
		t.Fatalf("the break flag did not survive the database")
	}
	if reloaded.PatternIndex != 3 {
		t.Fatalf("cycle position did not survive: %d", reloaded.PatternIndex)
	}
	if len(reloaded.Queue) != 1 || reloaded.Queue[0].Ref != "track:x" || reloaded.Queue[0].Of != 3 {
		t.Fatalf("the planned queue did not survive: %+v", reloaded.Queue)
	}

	decisions, err := RecentDecisions(ctx, db, "ch1", 5)
	if err != nil {
		t.Fatalf("recent decisions: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Selected == nil {
		t.Fatalf("the decision should have been recorded with its selection, got %+v", decisions)
	}
}

// Peeking must not spend anything. The preemption watchdog asks four times a
// minute, and a peek that consumed the listener's BACK instruction would eat it
// before the listener heard the result.
func TestPeekDoesNotWriteState(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustChannel(t, db, "ch1")
	mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourcePodcastSubscription, Label: "Show", Role: RoleTalk,
		Config: map[string]any{"podcastId": "p1"}, Enabled: boolPtr(true),
	})
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	skips := NewSkipRegistry(func() time.Time { return now })
	skips.PreferSource("ch1", "whatever")
	sched := NewScheduler(Dependencies{
		DB: db, Now: func() time.Time { return now }, Skips: skips,
		Catalog: &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
			"p1": {episode("e1", "An episode", now.Add(-3*time.Hour), 30)},
		}},
	})

	if _, err := sched.PeekItem(ctx, "ch1"); err != nil {
		t.Fatalf("peek: %v", err)
	}
	if skips.PreferredSource("ch1") == "" {
		t.Fatalf("a peek consumed the BACK instruction")
	}
	state, _ := LoadProgramState(ctx, db, "ch1")
	if state.BlockID != "" {
		t.Fatalf("a peek wrote programme state: %+v", state)
	}
	if decisions, _ := RecentDecisions(ctx, db, "ch1", 5); len(decisions) != 0 {
		t.Fatalf("a peek recorded a decision")
	}
}

func TestNextItemErrsWhenNoSources(t *testing.T) {
	db := newTestDB(t)
	mustChannel(t, db, "ch1")
	sched := NewScheduler(Dependencies{DB: db, Now: time.Now})
	if _, err := sched.NextItem(context.Background(), "ch1"); err == nil {
		t.Fatalf("expected an error for a channel with no sources")
	}
}

// A booked slot beats the rotation, and the derived plan is what turns a stored
// schedule rule into one.
func TestABookedSlotBeatsTheRotation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustChannel(t, db, "ch1")
	rotation := mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourcePodcastSubscription, Label: "Rotation", Role: RoleTalk,
		Config: map[string]any{"podcastId": "p1"}, Enabled: boolPtr(true),
	})
	show := mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourcePodcastSubscription, Label: "The Show", Role: RoleShow,
		Config: map[string]any{"podcastId": "p2"}, Enabled: boolPtr(true),
	})
	if _, err := InsertScheduleRule(ctx, db, "ch1", CreateScheduleRuleInput{
		SourceID: show.ID, Label: "The Show", WeekdayMask: 127,
		StartMinute: 16 * 60, EndMinute: 17 * 60, Enabled: boolPtr(true),
	}); err != nil {
		t.Fatalf("insert rule: %v", err)
	}

	now := time.Date(2026, 8, 10, 16, 30, 0, 0, time.UTC)
	sched := NewScheduler(Dependencies{
		DB: db, Now: func() time.Time { return now },
		Skips: NewSkipRegistry(func() time.Time { return now }),
		Catalog: &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
			"p1": {episode("rot", "Rotation item", now.Add(-40*24*time.Hour), 30)},
			"p2": {episode("show", "Today's show", now.Add(-24*time.Hour), 45)},
		}},
	})

	item, err := sched.NextItem(ctx, "ch1")
	if err != nil {
		t.Fatalf("next item: %v", err)
	}
	if item.SourceID != show.ID {
		t.Fatalf("the booked show should be on air at 16:30, got %q (source %s, rotation is %s)",
			item.Title, item.SourceID, rotation.ID)
	}
	if !item.IsRuleDriven {
		t.Fatalf("an item from a booked slot should be marked as such")
	}
	// The old engine cut in on the minute; a derived plan keeps that so nothing
	// changes for a channel nobody has re-planned.
	if item.AnchorPolicy != StartImmediately {
		t.Fatalf("a derived plan should preserve the old cut-in behaviour, got %q", item.AnchorPolicy)
	}
}

// ---- clocks ------------------------------------------------------------

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
		t.Fatalf("expected the highest-priority enabled rule for today, got %q", rule.ID)
	}
}

// A schedule is a bare minute-of-day, so it only means anything relative to a
// zone — and UTC shifts the WEEKDAY, not just the hour, which is how a Saturday
// 23:00 slot looks "not booked today".
func TestDependenciesLocationPrefersTheChannelThenTheDefault(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	deps := Dependencies{DefaultLocation: denver}
	if got := deps.location(Channel{}); got != denver {
		t.Fatalf("with no channel zone the default should apply, got %v", got)
	}
	if got := deps.location(Channel{Timezone: "UTC"}); got.String() != "UTC" {
		t.Fatalf("the channel's own zone should win, got %v", got)
	}
	// An unknown zone falls back rather than taking the channel off the air.
	if got := deps.location(Channel{Timezone: "Mars/Olympus"}); got != denver {
		t.Fatalf("an unparseable zone should fall back to the default, got %v", got)
	}
	bare := Dependencies{}
	if got := bare.location(Channel{}); got != time.UTC {
		t.Fatalf("with nothing configured the clock is UTC, got %v", got)
	}
}

func TestNormalizeRole(t *testing.T) {
	cases := []struct {
		role, kind string
		rotation   bool
		want       string
	}{
		{"", SourcePodcastSubscription, true, RoleTalk},
		{"", SourceMusicPlaylist, true, RoleMusic},
		{"", SourceFilePool, false, RoleShow},
		{"podcast", SourceFilePool, true, RoleTalk},
		{"filler", SourceFilePool, true, RoleMusic},
		{"COMMERCIAL", SourceFilePool, true, RoleCommercial},
		{"show", SourceLiveStream, true, RoleShow},
	}
	for _, tc := range cases {
		if got := NormalizeRole(tc.role, tc.kind, tc.rotation); got != tc.want {
			t.Fatalf("NormalizeRole(%q, %q, %v) = %q, want %q", tc.role, tc.kind, tc.rotation, got, tc.want)
		}
	}
}
