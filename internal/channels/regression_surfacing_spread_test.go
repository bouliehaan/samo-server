package channels

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// From the audit of 2026-09-13, on Jake's station: The Church of What's
// Happening Now, A tier, on a two-surfacing plan — new episodes every week,
// never once heard on the radio. It WAS airing. Both surfacings went out
// between 10:30 and 13:40 on weekdays, right behind the S-tier show, and by
// the evening the episode was retired as back catalogue. Three separate
// defects folded the second surfacing onto the first, and each of these pins
// one of them.

// spreadStation is a morning in the shape of the report: an S-tier show and an
// A-tier show with today's episode each, a shelf of other podcasts' back
// catalogue, songs for the breaks, and a new-episodes block whose pattern
// alternates a break with an obligation position.
func spreadStation(t *testing.T, now time.Time) *station {
	t.Helper()
	plan := Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 1}, {ID: "music", Target: 0}},
		ListeningDay: &DaySpec{Start: "08:00", End: "23:00"},
		Freshness:    FreshnessPolicy{Surfacings: map[string]int{"S": 2, "A": 2}},
		Pools: []Pool{
			{ID: "podcasts", Match: &PoolMatch{Kind: SourcePodcastSubscription}},
			{ID: "music", Match: &PoolMatch{Kind: SourceMusicPlaylist}},
		},
		Blocks: []Block{
			{ID: "general", Label: "General rotation", Default: true,
				Pools: []PoolRef{{Pool: "podcasts", Weight: 1}}},
			{ID: "fresh", Label: "New episodes",
				Enter:   BlockEntry{At: "08:00", Days: "*", When: "obligations.pending > 0"},
				Exit:    BlockExit{When: "obligations.pending == 0"},
				Next:    "general",
				Pools:   []PoolRef{{Pool: "podcasts", Weight: 1}},
				Pattern: []PatternStep{{Want: WantBreak}, {Want: WantObligation}},
				Breaks: &BreakPolicy{
					Between:  []CategoryID{"talk"},
					Target:   BreakSize{Duration: "6m", Items: 2},
					Accept:   BreakRange{Duration: []string{"3m", "9m"}, Items: []int{1, 2}},
					Elements: []BreakElement{{Pool: "music", Count: []int{1, 2}, Fill: true}},
				}},
		},
		UnderrunPool: "music",
	}

	mssp := podcastSource("mssp", "Matt and Shane", "p-mssp")
	mssp.Config["tier"] = "S"
	church := podcastSource("church", "The Church", "p-church")
	church.Config["tier"] = "A"
	sources := []Source{mssp, church, musicSource("mus1", "House", "pl1")}
	episodes := map[string][]catalog.PodcastEpisode{
		"p-mssp":   {episode("mssp-new", "MSSP new", now.Add(-3*time.Hour), 75)},
		"p-church": {episode("church-new", "Church new", now.Add(-4*time.Hour), 100)},
	}
	for index := 0; index < 8; index++ {
		id := "c" + strconv.Itoa(index)
		sources = append(sources, podcastSource(id, "Show "+id, "p-"+id))
		list := []catalog.PodcastEpisode{}
		for back := 0; back < 10; back++ {
			list = append(list, episode(id+"-old"+strconv.Itoa(back), "Show "+id+" archive "+strconv.Itoa(back),
				now.AddDate(0, 0, -30-back), 45))
		}
		episodes["p-"+id] = list
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 60; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%20), 180))
	}
	return newStation(t, plan, sources,
		&stubCatalog{episodes: episodes, playlists: map[string][]catalog.MusicTrack{"pl1": songs}}, now)
}

// A second surfacing lands somewhere ELSE in the day. Both of these went out
// again three hours after their first airing, with seven rules given up to do
// it, because the obligation position ran the relaxation ladder over the owed
// set alone and gave up item separation and the airing cap the moment nothing
// owed passed strictly.
func TestASecondSurfacingIsNotBurnedTheSameMorning(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	s := spreadStation(t, now)

	airings := map[string][]time.Time{}
	relaxed := 0
	for s.now.Before(now.Add(7 * time.Hour)) {
		item, decision := s.step()
		if item.Category == "talk" {
			airings[item.ItemRef] = append(airings[item.ItemRef], decision.At)
		}
		relaxed += len(decision.Relaxed)
	}
	for _, ref := range []string{"episode:mssp-new", "episode:church-new"} {
		times := airings[ref]
		if len(times) == 0 {
			t.Fatalf("%s never aired in the morning", ref)
		}
		if len(times) > 1 {
			t.Fatalf("%s aired %d times inside seven hours (%v): the second surfacing belongs to another part of the day",
				ref, len(times), times)
		}
	}
	if relaxed > 0 {
		t.Fatalf("%d rules were given up in a morning with a full shelf of back catalogue", relaxed)
	}
}

// The separation windows are fitted to the shelf, not to the owed set. With
// one show owed, "(distinct − 1) × typical" fitted the item window to zero and
// a thirty-minute episode owed twice went out at 09:06 and 09:42 with nothing
// in the record.
func TestSeparationIsFittedToTheShelfNotTheOwedSet(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	s := spreadStation(t, now)
	// Only one show has anything owed.
	s.engine.Catalog.(*stubCatalog).episodes["p-church"] = nil
	s.engine.Catalog.(*stubCatalog).episodes["p-mssp"] = []catalog.PodcastEpisode{
		episode("mssp-new", "MSSP new", now.Add(-3*time.Hour), 30),
	}

	airings := []time.Time{}
	for s.now.Before(now.Add(3 * time.Hour)) {
		item, decision := s.step()
		if item.ItemRef == "episode:mssp-new" {
			airings = append(airings, decision.At)
		}
	}
	if len(airings) != 1 {
		t.Fatalf("a single owed episode aired at %v — the window was fitted to a shelf of one", airings)
	}
}

// An owed giant is not a rested giant. A new three-hour B-tier episode marked
// its category "due" and swept every shorter new episode aside, so a one-hour
// S-tier episode from two hours ago went out after a giant from yesterday.
func TestAnOwedGiantDoesNotOutrankAHigherTier(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	s := spreadStation(t, now)
	rogan := podcastSource("rogan", "JRE", "p-rogan")
	rogan.Config["tier"] = "B"
	s.engine.Sources = append(s.engine.Sources, rogan)
	s.engine.Catalog.(*stubCatalog).episodes["p-rogan"] = []catalog.PodcastEpisode{
		episode("rogan-new", "JRE new", now.Add(-22*time.Hour), 180),
	}

	order := []string{}
	for s.now.Before(now.Add(6 * time.Hour)) {
		item, _ := s.step()
		if item.Category == "talk" {
			order = append(order, item.ItemRef)
		}
	}
	want := []string{"episode:mssp-new", "episode:church-new", "episode:rogan-new"}
	for index, ref := range want {
		if index >= len(order) || order[index] != ref {
			t.Fatalf("talk went out as %v, want the tiers in order %v", order, want)
		}
	}
}

// What a long item costs is measured against its own kind of programming. On
// a station that plays two songs between every podcast the overall median of
// recent airings is a song, and every episode looked like a giant.
func TestCommitmentIsJudgedAgainstTheCandidatesOwnCategory(t *testing.T) {
	tail := []PlayTailEntry{
		{Category: "music", Aired: 3 * time.Minute},
		{Category: "music", Aired: 3 * time.Minute},
		{Category: "talk", Aired: 55 * time.Minute},
		{Category: "music", Aired: 3 * time.Minute},
		{Category: "music", Aired: 3 * time.Minute},
		{Category: "talk", Aired: 45 * time.Minute},
	}
	env := scoreEnv{
		typicalItem:       typicalAired(tail),
		typicalByCategory: typicalAiredByCategory(tail),
		longFormThreshold: 2 * time.Hour,
	}
	if env.typicalItem >= 45*time.Minute {
		t.Fatalf("the fixture should make the station-wide median a song, got %s", env.typicalItem)
	}
	cost := env.commitment(Candidate{Category: "talk", Duration: 3 * time.Hour})
	// Three hours against a fifty-minute norm is a bit over three times the
	// commitment — log2 of that is under two — and nothing like the sixty-fold
	// reading the song median gave.
	if cost < -2 || cost > -1 {
		t.Fatalf("a three-hour episode on a station of ~50-minute episodes costs %.2f, want about -1.8", cost)
	}
}

// The most urgent owed thing in contention plays, not the best-scoring one.
func TestTheMostUrgentOwedContenderWins(t *testing.T) {
	scored := []ScoredCandidate{
		{Candidate: Candidate{Ref: "a", Owed: true, Urgency: 10.5}, Total: 4.6},
		{Candidate: Candidate{Ref: "s", Owed: true, Urgency: 12.5}, Total: 4.3},
		{Candidate: Candidate{Ref: "c", Owed: true, Urgency: 6.5}, Total: 4.2},
	}
	chosen, contenders := chooseCandidate(scored, 0.15, nil)
	if chosen.Candidate.Ref != "s" {
		t.Fatalf("chose %s among %d, want the S-tier episode", chosen.Candidate.Ref, len(contenders))
	}
}

// After a booked slot the station goes straight to the daypart claiming the
// hour, and a pattern that opens with a break steps over it when a break is
// what just played: one break at the join, not three songs across two.
func TestHandoverAfterASlotOpensWithOneBreak(t *testing.T) {
	now := time.Date(2026, 9, 14, 8, 55, 0, 0, time.UTC)
	s := spreadStation(t, now)
	s.engine.Plan.Blocks = append(s.engine.Plan.Blocks, Block{
		ID: "slot-news", Label: "News",
		Enter: BlockEntry{At: "08:00", Days: "*", Hard: true, Start: StartImmediately},
		Exit:  BlockExit{At: "09:00"},
		Pools: []PoolRef{{Pool: "news"}},
	})
	s.engine.Plan.Pools = append(s.engine.Plan.Pools, Pool{ID: "news", SourceIDs: []string{"news"}})
	s.engine.Sources = append(s.engine.Sources, Source{
		ID: "news", ChannelID: "ch1", Kind: SourceLiveStream, Label: "News", Enabled: true, Role: RoleShow,
		Config: map[string]any{"url": "http://example.test/news"},
	})
	if err := s.engine.Plan.Validate(); err != nil {
		t.Fatal(err)
	}

	sequence := []string{}
	for s.now.Before(now.Add(40 * time.Minute)) {
		item, decision := s.step()
		sequence = append(sequence, decision.BlockID+"/"+string(item.Category))
	}
	// News to 09:00, then the new-episodes block: its break, then the episode.
	want := []string{"slot-news/talk", "fresh/music", "fresh/music", "fresh/talk"}
	for index, expected := range want {
		if index >= len(sequence) || sequence[index] != expected {
			t.Fatalf("after the slot the station played %v, want %v", sequence, want)
		}
	}
}

// obligations.ready counts what is owed AND could air now; a second surfacing
// with separation still to run is owed and not ready.
func TestObligationsReadyExcludesWhatTheRulesHold(t *testing.T) {
	for _, raw := range []string{"obligations.ready > 0", "obligations.ready == 0"} {
		if _, err := ParseCondition(raw); err != nil {
			t.Fatalf("%q does not parse: %v", raw, err)
		}
	}
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	s := spreadStation(t, now)
	s.engine.Plan.Blocks[1].Exit.When = "obligations.ready == 0"
	s.engine.Catalog.(*stubCatalog).episodes["p-church"] = nil

	ctx := context.Background()
	env := s.env()
	timeline := BuildTimeline(s.engine.Plan, s.now, time.UTC)
	if ready := s.engine.readyObligations(ctx, s.now, timeline, nil, env); ready != 1 {
		t.Fatalf("one new episode owed and nothing in its way, ready = %d", ready)
	}
	// Air it once: still owed a second surfacing, but not ready for eight hours.
	for s.now.Before(now.Add(2 * time.Hour)) {
		s.play()
	}
	env = s.env()
	if pending := env.owed.Len(); pending != 1 {
		t.Fatalf("after one airing the episode should still be pending, got %d", pending)
	}
	tail, _ := s.history.Tail(ctx, 24*time.Hour, 200, s.now)
	timeline = BuildTimeline(s.engine.Plan, s.now, time.UTC)
	if ready := s.engine.readyObligations(ctx, s.now, timeline, tail, env); ready != 0 {
		t.Fatalf("a second surfacing inside its separation counted as ready (%d)", ready)
	}
}

// A booked slot's window belongs to the schedule; how the slot BEHAVES belongs
// to the plan. The reconcile rebuilt every slot block from its rule on every
// decision and kept only what the rule knows, so a plan that gave a booked
// show makeNext, an exposure or a grace was put back to the defaults each time
// the station decided anything.
func TestReconcileKeepsTheOwnersEditsOnASlotBlock(t *testing.T) {
	lofi := Source{ID: "lofi", ChannelID: "c", Kind: SourceLiveStream, Label: "Lofi Sleep",
		Enabled: true, Role: RoleShow, Config: map[string]any{"url": "http://example.test/lofi"}}
	rules := []ScheduleRule{{
		ID: "csched_lofi", ChannelID: "c", SourceID: "lofi", Label: "Lofi Sleep",
		WeekdayMask: 127, StartMinute: 23 * 60, EndMinute: 24 * 60, Enabled: true,
	}}
	half := 0.5
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "show-lofi", SourceIDs: []string{"lofi"}},
		},
		Blocks: []Block{
			{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "slot-csched_lofi", Label: "Lofi Sleep",
				// The owner moved the slot on the SCHEDULE to 22:30 (below); the
				// stale window here must lose. The rest is theirs and must win.
				Enter:    BlockEntry{At: "23:00", Days: "*", Hard: true, Start: StartMakeNext, Grace: "5m"},
				Exit:     BlockExit{At: "00:00"},
				Pools:    []PoolRef{{Pool: "show-lofi"}},
				Exposure: &half,
				Next:     "general",
				LongForm: &LongFormPolicy{Threshold: "3h", Rest: "7d"},
			},
		},
	}
	rules[0].StartMinute = 22*60 + 30

	reconciled, added, dropped := plan.ReconcileScheduleRules(rules, []Source{lofi})
	if len(added) != 0 || len(dropped) != 0 {
		t.Fatalf("nothing should be added (%v) or dropped (%v)", added, dropped)
	}
	block, ok := reconciled.Block("slot-csched_lofi")
	if !ok {
		t.Fatal("the slot block is gone")
	}
	if block.Enter.At != "22:30" {
		t.Fatalf("the window should follow the schedule, got %q", block.Enter.At)
	}
	if block.Enter.Start != StartMakeNext || block.Enter.Grace != "5m" {
		t.Fatalf("the owner's start policy was rewritten: %+v", block.Enter)
	}
	if block.Exposure == nil || *block.Exposure != 0.5 || block.Next != "general" || block.LongForm == nil {
		t.Fatalf("the owner's block settings were dropped: %+v", block)
	}
	if err := reconciled.Validate(); err != nil {
		t.Fatalf("the reconciled plan should be valid: %v", err)
	}
}

// A podcast added as a booked show, with no slot on the schedule, is owed and
// judged and never in any block's pool — and used to be reported reachable.
func TestABookedShowWithNoSlotIsReportedUnreachable(t *testing.T) {
	church := podcastSource("church", "The Church", "p-church")
	church.Role = RoleShow
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "podcasts", Match: &PoolMatch{Kind: SourcePodcastSubscription}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "podcasts"}}}},
	}
	if orphans := plan.UnreachableSources([]Source{church}); len(orphans) != 0 {
		t.Fatalf("a show is not a rotation source and must not fail a plan save: %v", orphans)
	}
	orphans := plan.UnreachableShows([]Source{church})
	if len(orphans) != 1 || orphans[0].ID != "church" {
		t.Fatalf("a show no slot names should be reported, got %v", orphans)
	}
	// Give it a slot and it is reached through the slot's own pool.
	rules := []ScheduleRule{{ID: "csched_church", ChannelID: "c", SourceID: "church",
		WeekdayMask: 127, StartMinute: 20 * 60, EndMinute: 22 * 60, Enabled: true}}
	reconciled, _, _ := plan.ReconcileScheduleRules(rules, []Source{church})
	if orphans := reconciled.UnreachableShows([]Source{church}); len(orphans) != 0 {
		t.Fatalf("a show with a slot is reachable, got %v", orphans)
	}
}

// An owed episode of a source no pool reaches is held by the plan, and says
// so; and the ready count does not count it. This is the shape a podcast
// added as a booked show with no slot takes: owed, judged, never enumerated.
func TestAnOwedEpisodeOfAnUnreachableSourceIsHeldNotFree(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	s := spreadStation(t, now)
	s.engine.Plan.Blocks[1].Exit.When = "obligations.ready == 0"
	for index := range s.engine.Sources {
		if s.engine.Sources[index].ID == "church" {
			s.engine.Sources[index].Role = RoleShow
		}
	}
	// Noticed first, as a decision would have; JudgeOwed is a peek.
	env := s.env()
	judged := s.engine.JudgeOwed(context.Background(), now, ProgramState{})
	church, ok := judged["episode:church-new"]
	if !ok {
		t.Fatal("the stranded episode should still be judged — it is owed")
	}
	if church.Held == nil || church.Held.Rule != "unreachable" {
		t.Fatalf("a stranded episode should be held by the plan, got %+v", church.Held)
	}
	mssp := judged["episode:mssp-new"]
	if mssp.Held != nil {
		t.Fatalf("the reachable episode should be free, got %+v", mssp.Held)
	}
	timeline := BuildTimeline(s.engine.Plan, now, time.UTC)
	if ready := s.engine.readyObligations(context.Background(), now, timeline, nil, env); ready != 1 {
		t.Fatalf("ready should count only the reachable episode, got %d", ready)
	}
}

// An obligation from a disabled source does not count as owed: a block gated
// on what is owed must not sit on air for a show the owner switched off.
func TestADisabledSourcesObligationsDoNotCount(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	s := spreadStation(t, now)
	env := s.env()
	if env.owed.Len() != 2 {
		t.Fatalf("two new episodes are owed, got %d", env.owed.Len())
	}
	// Disable the Church: its row stays, its claim on the station does not.
	kept := s.engine.Sources[:0:0]
	for _, src := range s.engine.Sources {
		if src.ID != "church" {
			kept = append(kept, src)
		}
	}
	s.engine.Sources = kept
	env = s.env()
	if env.owed.Len() != 1 {
		t.Fatalf("with the Church disabled only MSSP is owed, got %d", env.owed.Len())
	}
	if _, ok := env.owed.Get("episode:church-new"); ok {
		t.Fatal("a disabled source's episode is still in the queue")
	}
}
