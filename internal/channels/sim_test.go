package channels

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Three days of programming, in a second, with the reasoning for every choice
// attached. This is the test that would have caught the fifteen-hour talk
// marathon before it happened rather than after.

// simStation builds a station with enough content to run for days: two talk
// shows with back catalogues, a music playlist, and a booked hour every
// weekday morning.
func simStation(t *testing.T, start time.Time) *Engine {
	t.Helper()
	archive := func(prefix string, count, minutes int) []catalog.PodcastEpisode {
		out := make([]catalog.PodcastEpisode, 0, count)
		for i := 0; i < count; i++ {
			out = append(out, episode(
				prefix+string(rune('a'+i%26))+string(rune('a'+i/26)),
				prefix+" episode",
				start.Add(-time.Duration(30+i)*24*time.Hour),
				minutes,
			))
		}
		return out
	}
	// A realistic playlist: enough artists that a 90-minute separation is
	// satisfiable. With a dozen artists and twenty hours of music it is not,
	// and the engine would have to keep relaxing the rule — which it does, but
	// that is a fact about a thin library rather than about the scheduler.
	songs := make([]catalog.MusicTrack, 0, 120)
	for i := 0; i < 120; i++ {
		songs = append(songs, track(
			"t"+string(rune('a'+i%26))+string(rune('a'+(i/26)%26)),
			"Song", "Artist "+string(rune('A'+i%26))+string(rune('a'+(i/26)%26)), 180+i%7*20,
		))
	}

	plan := Plan{
		Version:    PlanVersion,
		Seed:       42,
		Categories: []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		Pools: []Pool{
			// Three shows, not two. At a 75% talk share, two shows cannot
			// satisfy a ninety-minute separation between the same host — each
			// would have to be on every other item — so the engine would
			// correctly relax the rule and correctly report that it had. That
			// is a fact about a thin library, and not what this is testing.
			{ID: "talk", SourceIDs: []string{"pod1", "pod2", "pod3"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
			{ID: "news", SourceIDs: []string{"news"}},
		},
		Blocks: []Block{
			{
				ID: "general", Label: "General rotation", Default: true,
				Pools: []PoolRef{{Pool: "talk", Weight: 1}, {Pool: "music", Weight: 1}},
				Limits: BlockLimits{
					MaxUnbroken: []CategoryLimit{{Category: "talk", Max: "90m", ResetAfter: "15m", MinItem: "20m"}},
					MinUnbroken: []CategoryMinRun{{Category: "music", Min: "20m", ResetAfter: "1m"}},
				},
			},
			{
				ID: "morning-news", Label: "Morning News",
				Enter: BlockEntry{At: "07:00", Days: "mon-fri", Hard: true, Start: StartMakeNext},
				Exit:  BlockExit{At: "08:00"},
				Pools: []PoolRef{{Pool: "news"}},
				Next:  "general",
			},
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("simulation plan invalid: %v", err)
	}

	return &Engine{
		Plan:    plan,
		Channel: Channel{ID: "sim", DayStartMinute: 8 * 60, DayEndMinute: 23 * 60},
		Sources: []Source{
			podcastSource("pod1", "Talk One", "p1"),
			podcastSource("pod2", "Talk Two", "p2"),
			podcastSource("pod3", "Talk Three", "p3"),
			musicSource("mus1", "House Playlist", "pl1"),
			podcastSource("news", "Morning News", "pnews"),
		},
		History: NewMemoryHistory(),
		Catalog: &stubCatalog{
			episodes: map[string][]catalog.PodcastEpisode{
				"p1":    archive("one", 30, 42),
				"p2":    archive("two", 30, 55),
				"p3":    archive("three", 30, 33),
				"pnews": archive("news", 20, 58),
			},
			playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		},
		Skips:    NewSkipRegistry(func() time.Time { return start }),
		Location: time.UTC,
	}
}

func TestSeventyTwoHoursOfProgrammingHoldsTogether(t *testing.T) {
	start := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC) // Monday 06:00
	engine := simStation(t, start)

	result, err := Simulate(context.Background(), engine, SimOptions{
		Start: start, Duration: 72 * time.Hour,
	})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	report := result.Report

	if report.Items < 200 {
		t.Fatalf("only %d items in 72 hours — something is stalling", report.Items)
	}
	if report.Gaps > 0 {
		t.Fatalf("%d moments with nothing to play:\n%s", report.Gaps, result.Format(false))
	}

	// The format, over three days, should land near what the plan asked for.
	shares := map[CategoryID]int{}
	for _, category := range report.Categories {
		shares[category.Category] = category.Percent
	}
	if shares["talk"] < 60 || shares["talk"] > 85 {
		t.Fatalf("talk share %d%% is nowhere near the 75%% target:\n%s", shares["talk"], result.Format(false))
	}
	if shares["music"] < 12 {
		t.Fatalf("music share %d%% — music is being starved:\n%s", shares["music"], result.Format(false))
	}

	// The thing that actually ruined a morning: an enormous unbroken run of one
	// kind of programming. The block's own limit is 90 minutes; one item may
	// overshoot it, so allow a generous margin and still catch a marathon.
	for _, run := range report.LongestRun {
		if run.Name == "talk" && run.Minutes > 160 {
			t.Fatalf("longest unbroken talk run was %dm:\n%s", run.Minutes, result.Format(false))
		}
	}

	// Every weekday morning slot should have gone out, on time.
	if len(report.Anchors) < 3 {
		t.Fatalf("expected three weekday news hours in 72 hours, got %d", len(report.Anchors))
	}
	for _, anchor := range report.Anchors {
		if anchor.Missed {
			t.Fatalf("the %s slot never went out:\n%s", anchor.Due.Format("Mon 15:04"), result.Format(false))
		}
		if anchor.LateBy != "" {
			late, err := time.ParseDuration(anchor.LateBy)
			if err == nil && late > 60*time.Minute {
				t.Fatalf("the %s slot started %s late", anchor.Due.Format("Mon 15:04"), anchor.LateBy)
			}
		}
	}

	// The whole three days, for anyone running this with -v. A scheduler is
	// judged by what it produces over days, and a pass/fail line does not show
	// you that.
	t.Log("\n" + result.Format(false))

	// Separation should hold, and hold without the engine having to give any
	// rule up: a library this size can satisfy every one of them.
	if report.BackToBackCreator > 0 {
		t.Fatalf("%d back-to-back items by the same person:\n%s",
			report.BackToBackCreator, result.Format(false))
	}
	if len(report.Relaxations) > 0 {
		t.Fatalf("the engine had to relax %v over three days with plenty of content:\n%s",
			report.Relaxations, result.Format(false))
	}
}

// Same seed, same three days. Without this the simulator cannot be used to
// compare two plans, because every run would differ for its own reasons.
func TestSimulationIsReproducible(t *testing.T) {
	start := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	run := func() []string {
		result, err := Simulate(context.Background(), simStation(t, start), SimOptions{
			Start: start, Duration: 24 * time.Hour, Seed: 7,
		})
		if err != nil {
			t.Fatalf("simulate: %v", err)
		}
		out := make([]string, 0, len(result.Steps))
		for _, step := range result.Steps {
			out = append(out, step.At.Format("15:04")+" "+step.Item.ItemRef)
		}
		return out
	}
	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatalf("runs produced %d and %d items", len(first), len(second))
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("runs diverged at %d: %q vs %q", index, first[index], second[index])
		}
	}
}

// A different seed should produce a different station that is still a valid
// one. Controlled randomness, not chaos and not a fixed loop.
func TestADifferentSeedProducesADifferentButValidDay(t *testing.T) {
	start := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	runWith := func(seed int64) SimResult {
		result, err := Simulate(context.Background(), simStation(t, start), SimOptions{
			Start: start, Duration: 24 * time.Hour, Seed: seed,
		})
		if err != nil {
			t.Fatalf("simulate: %v", err)
		}
		return result
	}
	a, b := runWith(11), runWith(9999)
	if a.Report.Gaps > 0 || b.Report.Gaps > 0 {
		t.Fatalf("a seed change should not produce dead air")
	}

	same := 0
	limit := len(a.Steps)
	if len(b.Steps) < limit {
		limit = len(b.Steps)
	}
	for index := 0; index < limit; index++ {
		if a.Steps[index].Item.ItemRef == b.Steps[index].Item.ItemRef {
			same++
		}
	}
	if limit > 0 && same == limit {
		t.Fatalf("two different seeds produced an identical day — the randomness is not reaching the choice")
	}
}

// Three days of a station that actually behaves like one: overnight releases
// that wait for the morning, a fresh-podcast cycle with breaks between the
// episodes, and general programming after it.
func TestThreeDaysOfAStationWithFreshContentAndBreaks(t *testing.T) {
	start := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC) // Monday, before the day starts

	// Two shows that publish overnight, at different tiers, plus a back
	// catalogue and a real playlist.
	midnight := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	fresh := func(prefix string, tier string, days int, hour int, minutes int) []catalog.PodcastEpisode {
		out := []catalog.PodcastEpisode{}
		for day := 0; day < days; day++ {
			at := midnight.AddDate(0, 0, day).Add(time.Duration(hour) * time.Hour)
			out = append(out, episode(prefix+"-new-"+strconv.Itoa(day),
				prefix+" episode "+strconv.Itoa(day), at, minutes))
		}
		for index := 0; index < 12; index++ {
			out = append(out, episode(prefix+"-old-"+strconv.Itoa(index),
				prefix+" archive "+strconv.Itoa(index),
				start.AddDate(0, 0, -30-index), minutes))
		}
		_ = tier
		return out
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 120; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%40), 190+index%9*15))
	}

	top := podcastSource("pod1", "Top Show", "p1")
	top.Config["tier"] = "S"
	second := podcastSource("pod2", "Second Show", "p2")
	second.Config["tier"] = "B"

	plan := Plan{
		Version:      PlanVersion,
		Seed:         99,
		Categories:   []CategoryDef{{ID: "talk", Target: 0.7}, {ID: "music", Target: 0.3}},
		ListeningDay: &DaySpec{Start: "08:00", End: "23:00"},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"pod1", "pod2"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
		},
		Blocks: []Block{
			{
				ID: "overnight", Label: "Overnight",
				Enter: BlockEntry{At: "23:00", Days: "*"},
				Exit:  BlockExit{At: "08:00"},
				Pools: []PoolRef{{Pool: "talk"}, {Pool: "music"}},
				// Nothing aired here reaches anybody, so nothing aired here
				// spends a new episode.
				Exposure: func() *float64 { zero := 0.0; return &zero }(),
				Next:     "fresh-cycle",
			},
			{
				ID: "fresh-cycle", Label: "Fresh Podcasts",
				Enter:   BlockEntry{At: "08:00", Days: "*", When: "obligations.pending > 0"},
				Exit:    BlockExit{When: "obligations.pending == 0"},
				Pattern: []PatternStep{{Want: WantObligation}, {Want: WantBreak}},
				Pools:   []PoolRef{{Pool: "talk"}, {Pool: "music"}},
				Breaks: &BreakPolicy{
					Target:   BreakSize{Duration: "7m", Items: 2},
					Accept:   BreakRange{Duration: []string{"3m", "12m"}, Items: []int{1, 3}},
					Elements: []BreakElement{{Pool: "music", Count: []int{1, 3}, Fill: true}},
				},
				Next: "general",
			},
			{
				ID: "general", Label: "General rotation", Default: true,
				Pools: []PoolRef{{Pool: "talk"}, {Pool: "music"}},
				Limits: BlockLimits{
					MaxUnbroken: []CategoryLimit{{Category: "talk", Max: "90m", ResetAfter: "15m", MinItem: "20m"}},
					MinUnbroken: []CategoryMinRun{{Category: "music", Min: "20m", ResetAfter: "1m"}},
				},
			},
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan: %v", err)
	}

	engine := &Engine{
		Plan:        plan,
		Channel:     Channel{ID: "sim"},
		Sources:     []Source{top, second, musicSource("mus1", "House Playlist", "pl1")},
		History:     NewMemoryHistory(),
		Obligations: NewMemoryObligations(),
		Catalog: &stubCatalog{
			episodes: map[string][]catalog.PodcastEpisode{
				"p1": fresh("top", "S", 3, 4, 40),    // drops at 04:00
				"p2": fresh("second", "B", 3, 6, 35), // drops at 06:00
			},
			playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		},
		Skips:    NewSkipRegistry(func() time.Time { return start }),
		Location: time.UTC,
	}

	result, err := Simulate(context.Background(), engine, SimOptions{Start: start, Duration: 72 * time.Hour})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	report := result.Report
	t.Log("\n" + result.Format(false))

	if report.Gaps > 0 {
		t.Fatalf("%d moments with nothing to play", report.Gaps)
	}
	// Six overnight releases across three days, and the station should have got
	// to all of them.
	if report.Obligations.Surfaced < 5 {
		t.Fatalf("only %d of six new episodes were surfaced in three days", report.Obligations.Surfaced)
	}
	if report.Obligations.StillOwed > 1 {
		t.Fatalf("%d episodes were never got to", report.Obligations.StillOwed)
	}
	// The whole point of holding an overnight drop: it goes out in the morning,
	// not at four.
	for _, step := range result.Steps {
		if step.Decision.Selected == nil || !step.Decision.Selected.Owed {
			continue
		}
		if hour := step.At.Hour(); hour < 8 || hour >= 23 {
			t.Fatalf("a new episode was surfaced at %s, outside the listening day:\n%s",
				step.At.Format("Mon 15:04"), step.Decision.Explain())
		}
	}
	// And breaks actually happened, at roughly the shape asked for.
	if report.Breaks.Count < 3 {
		t.Fatalf("expected breaks between the fresh episodes, got %d", report.Breaks.Count)
	}
	if report.Breaks.MeanMinutes < 3 || report.Breaks.MeanMinutes > 12 {
		t.Fatalf("breaks averaged %.1fm, outside the 3–12m accept range", report.Breaks.MeanMinutes)
	}
	if report.BackToBackCreator > 0 {
		t.Fatalf("%d back-to-back items by the same person", report.BackToBackCreator)
	}
}

// The S-tier show published at 04:00 should be the first thing surfaced when
// the day starts, ahead of the B-tier one published two hours later.
func TestTheMoreImportantShowGoesFirstInTheMorning(t *testing.T) {
	start := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	top := podcastSource("pod1", "Top Show", "p1")
	top.Config["tier"] = "S"
	second := podcastSource("pod2", "Second Show", "p2")
	second.Config["tier"] = "B"

	plan := twoCategoryPlan(0.75)
	plan.ListeningDay = &DaySpec{Start: "08:00", End: "23:00"}
	cat := &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{
			// S tier, six hours old. B tier, ten minutes old.
			"p1": {episode("mssp", "The important one", start.Add(-6*time.Hour), 40)},
			"p2": {episode("gecko", "The other one", start.Add(-10*time.Minute), 40)},
		},
		playlists: map[string][]catalog.MusicTrack{"pl1": {
			track("t1", "Song one", "Artist A", 200),
			track("t2", "Song two", "Artist B", 200),
		}},
	}
	s := newStation(t, plan, []Source{top, second, musicSource("mus1", "House", "pl1")}, cat, start)

	item, decision := s.decide()
	if item.ItemRef != "episode:mssp" {
		t.Fatalf("the S-tier show from six hours ago should go first, got %q\n%s",
			item.Title, decision.Explain())
	}
}

// The station starts the morning after a long night of talk — the exact state
// that produced the complaint — and must recover rather than continue.
func TestTheStationRecoversFromANightOfTalk(t *testing.T) {
	start := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	engine := simStation(t, start)

	result, err := Simulate(context.Background(), engine, SimOptions{
		Start:    start,
		Duration: 4 * time.Hour,
		Warmup: []MemoryPlay{{
			SourceID: "pod1", ItemRef: "warmup:night", Category: "talk",
			StartedAt: start.Add(-8 * time.Hour), EndedAt: start,
			DurationSeconds: 8 * 60 * 60,
		}},
	})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	if len(result.Steps) == 0 {
		t.Fatalf("nothing played")
	}
	if got := result.Steps[0].Item.Category; got != "music" {
		t.Fatalf("after eight hours of talk the station opened with %s (%q):\n%s",
			got, result.Steps[0].Item.Title, result.ExplainStep(0))
	}

	// And it should not simply flip to eight hours of music either.
	for _, run := range result.Report.LongestRun {
		if run.Name == "music" && run.Minutes > 90 {
			t.Fatalf("over-corrected into a %dm music block:\n%s", run.Minutes, result.Format(false))
		}
	}
}
