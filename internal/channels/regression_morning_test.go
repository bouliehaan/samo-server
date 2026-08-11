package channels

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The morning of 2026-08-10, as reported: the booked news hour ended and the
// station played a great deal of music, and when it finally came back to spoken
// word it played OLD episodes while several new ones were sitting there.
//
// Reproduced before anything is changed, because the three plausible causes —
// the run limit, the balance correction, and separation — produce the same
// symptom from the outside and only the decision record can tell them apart.

// reportedMorning is the station as described: an overnight block worth no
// exposure, a booked news hour, and general programming after it. Several
// episodes drop overnight and get aired to nobody before the day starts.
func reportedMorning(t *testing.T, now time.Time) *station {
	t.Helper()
	overnightDrop := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)

	newEpisodes := []catalog.PodcastEpisode{
		episode("new-a", "Today's episode A", overnightDrop, 62),
		episode("new-b", "Today's episode B", overnightDrop.Add(30*time.Minute), 48),
	}
	archiveA := []catalog.PodcastEpisode{}
	archiveB := []catalog.PodcastEpisode{}
	for index := 0; index < 10; index++ {
		archiveA = append(archiveA, episode("old-a"+strconv.Itoa(index),
			"Archive A "+strconv.Itoa(index), now.AddDate(0, 0, -40-index), 55))
		archiveB = append(archiveB, episode("old-b"+strconv.Itoa(index),
			"Archive B "+strconv.Itoa(index), now.AddDate(0, 0, -60-index), 44))
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 60; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%20), 210))
	}

	showA := podcastSource("pod-a", "Show A", "pa")
	showA.Config["tier"] = "S"
	showB := podcastSource("pod-b", "Show B", "pb")
	showB.Config["tier"] = "A"
	news := podcastSource("news", "Morning News", "pnews")
	news.Role = RoleShow

	plan := Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		ListeningDay: &DaySpec{Start: "08:00", End: "23:00"},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"pod-a", "pod-b"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
			{ID: "news", SourceIDs: []string{"news"}},
		},
		Blocks: []Block{
			{
				ID: "general", Label: "General rotation", Default: true,
				Pools: []PoolRef{{Pool: "talk"}, {Pool: "music"}},
				Limits: BlockLimits{
					MaxUnbroken: []CategoryLimit{{Category: "talk", Max: "90m", ResetAfter: "15m", MinItem: "20m"}},
					MinUnbroken: []CategoryMinRun{{Category: "music", Min: "20m", ResetAfter: "1m"}},
				},
			},
			{
				ID: "morning-news", Label: "Morning News",
				Enter: BlockEntry{At: "08:00", Days: "*", Hard: true, Start: StartMakeNext},
				Exit:  BlockExit{At: "09:00"},
				Pools: []PoolRef{{Pool: "news"}},
				Next:  "general",
			},
		},
	}

	s := newStation(t, plan, []Source{showA, showB, news, musicSource("mus1", "House", "pl1")},
		&stubCatalog{
			episodes: map[string][]catalog.PodcastEpisode{
				"pa":    append(append([]catalog.PodcastEpisode{}, newEpisodes[0]), archiveA...),
				"pb":    append(append([]catalog.PodcastEpisode{}, newEpisodes[1]), archiveB...),
				"pnews": {episode("news-today", "Morning Edition", now.Add(-2*time.Hour), 58)},
			},
			playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		}, now)
	return s
}

// THE COMPLAINT. At 09:00, with the news hour just finished and two new
// episodes owed, the station should come back to spoken word and it should be
// the NEW episodes — not the back catalogue, and not forty minutes of music
// first.
func TestAfterTheBookedHourTheNewEpisodesGoOut(t *testing.T) {
	morning := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	s := reportedMorning(t, morning)

	// Overnight: the station was on, and it aired both new episodes to nobody.
	// The listening day says that reaches no one, so both are still owed.
	s.history.Record(MemoryPlay{
		SourceID: "pod-a", ItemRef: "episode:new-a", Category: "talk",
		StartedAt: morning.Add(-5 * time.Hour), EndedAt: morning.Add(-4 * time.Hour),
		DurationSeconds: 62 * 60,
	})
	s.history.Record(MemoryPlay{
		SourceID: "pod-b", ItemRef: "episode:new-b", Category: "talk",
		StartedAt: morning.Add(-4 * time.Hour), EndedAt: morning.Add(-3 * time.Hour),
		DurationSeconds: 48 * 60,
	})
	// Then the booked news hour, 08:00 to 09:00.
	s.history.Record(MemoryPlay{
		SourceID: "news", ItemRef: "episode:news-today", Category: "talk",
		StartedAt: morning.Add(-time.Hour), EndedAt: morning,
		DurationSeconds: 58 * 60,
	})

	// Both should still be owed: nothing that aired reached anybody.
	queue := s.env().owed
	if !queue.Owes("episode:new-a") || !queue.Owes("episode:new-b") {
		t.Fatalf("both overnight releases should still be owed, got %+v", queue.Pending)
	}

	// Now: what goes out at 09:00, and for the next hour?
	played := []string{}
	music := 0
	surfacedNew := false
	for i := 0; i < 12; i++ {
		item, decision := s.step()
		played = append(played, item.ItemRef)
		if item.Category == "music" {
			music++
		}
		if item.ItemRef == "episode:new-a" || item.ItemRef == "episode:new-b" {
			surfacedNew = true
			break
		}
		if i == 0 && item.Category == "music" {
			t.Logf("first item after the news hour was music:\n%s", decision.Explain())
		}
	}
	if !surfacedNew {
		t.Fatalf("twelve items after the news hour and neither new episode aired: %v", played)
	}
	if music > 0 {
		t.Fatalf("played %d music items before getting to a new episode: %v", music, played)
	}
	// And it is the more important show that goes first, not whichever the
	// weighted pick happened to like: a tier that only usually wins is not a
	// tier.
	if played[len(played)-1] != "episode:new-a" {
		_, d := s.decide()
		t.Fatalf("the S-tier episode should have led, got %v\n%s", played, d.Explain())
	}
}

// THE ONE THAT ACTUALLY BIT. A podcast added to the channel after the plan was
// written belonged to no pool, so the scheduler could not see it — while the
// mix screen said ENABLED, the obligation queue said the station owed you its
// episodes, and it never played. Pools were a snapshot of the library at save
// time.
func TestASourceAddedAfterThePlanStillPlays(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	existing := podcastSource("pod1", "Old Show", "p1")
	// Added later, top tier, brand new episode: the exact case.
	added := podcastSource("pod2", "Matt and Shane's Secret Podcast", "p2")
	added.Config["tier"] = "S"

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		Pools: []Pool{
			// A rule, not a list. This is the fix.
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
		},
		Blocks: []Block{{
			ID: "general", Default: true,
			Pools: []PoolRef{{Pool: "talk"}, {Pool: "music"}},
		}},
	}
	cat := &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{
			"p1": {episode("old1", "Back catalogue", now.AddDate(0, 0, -40), 25)},
			"p2": {episode("brand-new", "Ep 630 - Buildings", now.Add(-4*time.Hour), 90)},
		},
		playlists: map[string][]catalog.MusicTrack{"pl1": {track("t1", "Song", "Artist A", 200)}},
	}
	s := newStation(t, plan, []Source{existing, added, musicSource("mus1", "Easy Listening", "pl1")}, cat, now)

	if orphans := plan.UnreachableSources(s.engine.Sources); len(orphans) != 0 {
		t.Fatalf("a matched pool should reach every talk source, orphans: %+v", orphans)
	}
	item, decision := s.decide()
	if item.ItemRef != "episode:brand-new" {
		t.Fatalf("the S-tier episode from four hours ago should have played, got %q\n%s",
			item.Title, decision.Explain())
	}
}

// And the shape that caused it must now be impossible to save.
func TestAPlanThatCannotReachASourceIsRefused(t *testing.T) {
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{"pod1"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
	}
	sources := []Source{
		{ID: "pod1", Kind: SourcePodcastSubscription, Enabled: true, Role: RoleTalk},
		{ID: "pod2", Label: "Added later", Kind: SourcePodcastSubscription, Enabled: true, Role: RoleTalk},
		// A booked show reaches air through its own block, not a rotation pool.
		{ID: "show1", Kind: SourceInternetStation, Enabled: true, Role: RoleShow},
		// A disabled source is not content the station is failing to play.
		{ID: "off", Kind: SourcePodcastSubscription, Enabled: false, Role: RoleTalk},
	}
	orphans := plan.UnreachableSources(sources)
	if len(orphans) != 1 || orphans[0].ID != "pod2" {
		t.Fatalf("expected exactly the later-added podcast, got %+v", orphans)
	}

	// Switching the pool to a rule fixes it without listing anything.
	plan.Pools[0] = Pool{ID: "talk", Match: &PoolMatch{Category: "talk"}}
	if orphans := plan.UnreachableSources(sources); len(orphans) != 0 {
		t.Fatalf("a matched pool should reach everything of that category, got %+v", orphans)
	}
}

// Setting a tier has to apply to the episodes already waiting, or the rating
// does nothing anybody can see.
func TestChangingATierUpdatesWhatIsAlreadyOwed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	store := NewMemoryObligations()
	first := Obligation{
		ItemRef: "episode:one", Tier: DefaultTier, Title: "Ep 630",
		PublishedAt: now.Add(-4 * time.Hour), ExpiresAt: now.Add(68 * time.Hour),
		State: ObligationPending,
	}
	if err := store.Notice(ctx, []Obligation{first}, now); err != nil {
		t.Fatalf("notice: %v", err)
	}
	if err := store.Credit(ctx, "episode:one", 0.4, now); err != nil {
		t.Fatalf("credit: %v", err)
	}

	// The show is re-rated, and the feed is read again.
	upgraded := first
	upgraded.Tier = TierS
	if err := store.Notice(ctx, []Obligation{upgraded}, now.Add(time.Minute)); err != nil {
		t.Fatalf("re-notice: %v", err)
	}

	stored, _ := store.List(ctx, now.Add(time.Minute))
	if len(stored) != 1 {
		t.Fatalf("expected one obligation, got %d", len(stored))
	}
	if stored[0].Tier != TierS {
		t.Fatalf("the new tier should apply to what is already owed, got %s", stored[0].Tier)
	}
	if stored[0].Credit != 0.4 {
		t.Fatalf("re-noticing must not touch credit, got %v", stored[0].Credit)
	}
}

// "It's playing fucking old podcasts over new podcasts. that should never ever
// ever happen when there are podcasts owed to me."
//
// The mechanism was not scoring. With a ninety-minute talk limit and sixty-two
// minutes of run already gone, every owed episode of forty-five minutes or more
// failed the remaining-run ceiling, and the ONLY spoken items short enough to
// fit were short back-catalogue ones. The limit had quietly become a filter
// that selects old content over new, every time a talk run nears its end.
func TestBackCatalogueNeverBeatsSomethingOwed(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 17, 0, 0, time.UTC)

	owedShow := podcastSource("pod-new", "History of Everything", "pnew")
	shortArchive := podcastSource("pod-old", "Planet Money", "pold")

	archive := []catalog.PodcastEpisode{}
	for index := 0; index < 40; index++ {
		// Short, plentiful, and old — exactly what used to win.
		archive = append(archive, episode("pm"+strconv.Itoa(index),
			"How to start a bank "+strconv.Itoa(index), now.AddDate(0, 0, -60-index), 26))
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 30; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%10), 200))
	}

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
		},
		Blocks: []Block{{
			ID: "general", Default: true,
			Pools: []PoolRef{{Pool: "talk"}, {Pool: "music"}},
			Limits: BlockLimits{
				MaxUnbroken: []CategoryLimit{{Category: "talk", Max: "90m", ResetAfter: "15m", MinItem: "20m"}},
			},
		}},
	}
	cat := &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{
			// 48 minutes: longer than what is left of the run, and OWED.
			"pnew": {episode("powder-kegs", "The Powder Kegs That Led to the American Revolution",
				now.Add(-9*time.Hour), 48)},
			"pold": archive,
		},
		playlists: map[string][]catalog.MusicTrack{"pl1": songs},
	}
	s := newStation(t, plan, []Source{owedShow, shortArchive, musicSource("mus1", "Easy Listening", "pl1")}, cat, now)

	// Sixty-two minutes into the talk run, as it was.
	s.state = ProgramState{BlockID: "general", EnteredAt: now.Add(-70 * time.Minute)}
	s.history.Record(MemoryPlay{
		SourceID: "pod-old", ItemRef: "episode:pm99", Category: "talk",
		StartedAt: now.Add(-62 * time.Minute), EndedAt: now, DurationSeconds: 62 * 60,
	})

	if !s.env().owed.Owes("episode:powder-kegs") {
		t.Fatalf("the nine-hour-old episode should be owed")
	}

	// The invariant is about OLD TALK, not about playing the episode this very
	// second. With twenty-eight minutes left in the run and a forty-eight
	// minute episode owed, the right answer is to end the talk run — play the
	// music, then the episode goes out WHOLE into a fresh run. What must never
	// happen is a five-year-old Planet Money rerun going out because it was the
	// only spoken thing short enough to fit.
	//
	// Letting the owed episode simply ignore the ceiling was the previous
	// answer, and it had its own cost: the run then ran to twice its limit,
	// which is the marathon the ceiling exists to prevent.
	var aired []PlaybackItem
	for step := 0; step < 12; step++ {
		item, decision := s.step()
		aired = append(aired, item)
		if item.SourceID == "pod-old" {
			t.Fatalf("played back catalogue %q over an owed episode — old must never beat owed\n%s",
				item.Title, decision.Explain())
		}
		if item.ItemRef == "episode:powder-kegs" {
			if item.DurationSeconds != 48*60 {
				t.Fatalf("the owed episode went out as %ds, not whole", item.DurationSeconds)
			}
			return
		}
	}
	titles := make([]string, 0, len(aired))
	for _, item := range aired {
		titles = append(titles, item.Title)
	}
	t.Fatalf("the owed episode never went out; the station played: %s", strings.Join(titles, ", "))
}

// And the guard: this must not become the fifteen-hour talk marathon again.
// Owed outranks back catalogue WITHIN a category; which category plays is still
// the balance's call, so music still breaks up a long talk run.
func TestOwedContentStillYieldsToTheBalance(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	owedShow := podcastSource("pod-new", "New Show", "pnew")
	episodes := []catalog.PodcastEpisode{}
	for index := 0; index < 8; index++ {
		episodes = append(episodes, episode("n"+strconv.Itoa(index),
			"New episode "+strconv.Itoa(index), now.Add(-time.Duration(index+1)*time.Hour), 40))
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 30; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%10), 200))
	}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
		},
		Blocks: []Block{{
			ID: "general", Default: true,
			Pools: []PoolRef{{Pool: "talk"}, {Pool: "music"}},
			Limits: BlockLimits{
				MaxUnbroken: []CategoryLimit{{Category: "talk", Max: "90m", ResetAfter: "15m", MinItem: "20m"}},
				MinUnbroken: []CategoryMinRun{{Category: "music", Min: "20m", ResetAfter: "1m"}},
			},
		}},
	}
	s := newStation(t, plan, []Source{owedShow, musicSource("mus1", "Easy", "pl1")},
		&stubCatalog{
			episodes:  map[string][]catalog.PodcastEpisode{"pnew": episodes},
			playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		}, now)

	categories := map[CategoryID]int{}
	longestTalk, run := 0, 0
	for i := 0; i < 16; i++ {
		item := s.play()
		categories[item.Category]++
		if item.Category == "talk" {
			run++
			if run > longestTalk {
				longestTalk = run
			}
		} else {
			run = 0
		}
	}
	if categories["music"] == 0 {
		t.Fatalf("eight owed episodes must not lock music out entirely: %v", categories)
	}
	if longestTalk > 4 {
		t.Fatalf("%d spoken items in a row — the marathon is back", longestTalk)
	}
}

// A booked block with no stated end runs until the NEXT booked thing, which on
// a station with one booked show is the whole day — and from the outside that
// is "it never switches away from the scheduled programme". Nothing in the
// editor forced an end time, so the plan has to.
func TestABookedBlockMustSayWhenItEnds(t *testing.T) {
	plan := validPlan()
	plan.Blocks = append(plan.Blocks, Block{
		ID: "morning-news", Label: "Morning News",
		Enter: BlockEntry{At: "08:00", Days: "*", Hard: true},
		Pools: []PoolRef{{Pool: "talk"}},
	})
	err := plan.Validate()
	if err == nil || !strings.Contains(err.Error(), "never says when it ends") {
		t.Fatalf("a booked block with no end must be refused, got %v", err)
	}

	// And any of the ways of saying it is enough.
	for _, exit := range []BlockExit{
		{At: "09:00"}, {Duration: "1h"}, {AtNextAnchor: true}, {Count: 3},
	} {
		plan.Blocks[len(plan.Blocks)-1].Exit = exit
		if err := plan.Validate(); err != nil {
			t.Fatalf("exit %+v should be accepted: %v", exit, err)
		}
	}
}

// A plan saved before that rule existed must not take the station off the air:
// it falls back to the derived plan, loudly.
func TestAnUnusableStoredPlanFallsBackRatherThanFailing(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustChannel(t, db, "ch1")
	mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourcePodcastSubscription, Label: "Show", Role: RoleTalk,
		Config: map[string]any{"podcastId": "p1"}, Enabled: boolPtr(true),
	})
	// Written straight to the table, as an older version of the engine would
	// have accepted it.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO channel_programming_plan (channel_id, plan_json, version, updated_at)
		VALUES (?, ?, 1, ?)`, "ch1",
		`{"version":1,"categories":[{"id":"talk","target":1}],
		  "pools":[{"id":"talk","sourceIds":["x"]}],
		  "blocks":[{"id":"g","default":true,"pools":[{"pool":"talk"}]},
		            {"id":"news","enter":{"at":"08:00","hard":true},"pools":[{"pool":"talk"}]}]}`,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed plan: %v", err)
	}

	sched := NewScheduler(Dependencies{DB: db, Now: time.Now})
	plan, err := sched.PlanFor(ctx, Channel{ID: "ch1"}, []Source{
		{ID: "s1", Kind: SourcePodcastSubscription, Enabled: true, Role: RoleTalk},
	})
	if err != nil {
		t.Fatalf("an unusable stored plan should fall back, not fail: %v", err)
	}
	if _, ok := plan.Block("general"); !ok {
		t.Fatalf("expected the derived plan, got blocks %+v", plan.Blocks)
	}
}

// An item whose length nobody knows must not be allowed to run over a booked
// show. Feed episodes routinely report no duration, and a fit rule that waves
// them through is a fit rule that does not apply to podcasts.
func TestAnUnmeasuredItemIsNotStartedInFrontOfAnAnchor(t *testing.T) {
	beforeNews := time.Date(2026, 8, 10, 7, 50, 0, 0, time.UTC)
	s := reportedMorning(t, beforeNews)
	// Everything this station owns reports no duration, as a feed often does.
	for _, episodes := range s.engine.Catalog.(*stubCatalog).episodes {
		for index := range episodes {
			episodes[index].DurationSeconds = 0
		}
	}

	item, decision := s.decide()
	if decision.NextAnchor == nil {
		t.Fatalf("the 08:00 news hour should be visible at 07:50")
	}
	if item.MaxDuration == 0 || item.MaxDuration > 10*time.Minute {
		t.Fatalf("with ten minutes until a booked show, an item of unknown length was started "+
			"with no ceiling (MaxDuration %s) — it will run over the hour\n%s",
			item.MaxDuration, decision.Explain())
	}
}

// Tonight's live failure, 2026-08-10 20:20: five episodes owed, ninety-nine
// minutes until a booked show, and the station played a five-year-old Planet
// Money rerun. Three of the five FITTED the window (61m, 94m, 86m).
//
// The defect is an asymmetry, and this asserts it directly rather than trying
// to guess which rule fired on the night. The gate on "can what is owed air"
// ran ONE strict constraint pass. The ordinary programming that replaces it
// goes through the relaxing path. So a rule the engine is perfectly willing to
// give up for a five-year-old rerun was enough to disqualify a new episode —
// new episode refused for touching a rule, old episode allowed to bend it.
//
// Whatever the rule happens to be, the two questions must be asked the same
// way, or "old must never beat owed" is decided by which code path you took.
func TestOwedIsJudgedTheSameWayAsWhatWouldReplaceIt(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 20, 0, 0, time.UTC)

	// The five that were owed on the night, from five different shows, with
	// their real durations.
	shows := []struct {
		id      string
		title   string
		minutes int
	}{
		{"pod-msspod", "Ep 629 - Like and Subscribe", 61},
		{"pod-dillon", "#193 - Chris Distefano", 94},
		{"pod-wan", "My Family Left Me - WAN Show", 239},
		{"pod-dough", "Wainscotting, Hold The Hamm", 86},
		{"pod-ryan", "Scott Payne", 230},
	}
	owed := []Candidate{}
	lastSource := map[string]time.Time{}
	lastCreator := map[string]time.Time{}
	for _, show := range shows {
		owed = append(owed, Candidate{
			Ref: "episode:" + show.id, Title: show.title, SourceID: show.id,
			Category: "talk", Duration: time.Duration(show.minutes) * time.Minute,
			Owed: true, Creator: show.id, Show: "podcast:" + show.id,
			Traits: Traits{HasCreator: true, SharedCreator: true, SupportsFreshness: true},
		})
		// Every one of them was on earlier today, which is what tripped the
		// separation: the station had been playing their back catalogue.
		lastSource[show.id] = now.Add(-time.Hour)
		lastCreator[show.id] = now.Add(-time.Hour)
	}

	env := constraintEnv{
		now:               now,
		window:            99 * time.Minute,
		lastBySource:      lastSource,
		lastByCreator:     lastCreator,
		lastByShow:        map[string]time.Time{},
		lastByRef:         map[string]time.Time{},
		airings:           map[string]int{},
		lastAirings:       map[string]time.Time{},
		listened:          map[string]bool{},
		separationSource:  8 * time.Hour,
		separationCreator: 8 * time.Hour,
		categoriesPresent: map[CategoryID]int{"talk": 1},
	}

	survivors, _, relaxed := applyConstraints(owed, env)
	if len(survivors) == 0 {
		t.Fatal("the selection path itself could not air the owed episode")
	}
	t.Logf("the selection path airs it after giving up: %v", relaxed)

	if !anyQualify(owed, env) {
		t.Fatal("the owed-content gate refused an episode the selection path would have aired — " +
			"that asymmetry is what plays old podcasts over new ones")
	}
}

// And the rule that must NOT bend: a four-hour episode still cannot start
// ninety-nine minutes before a booked show.
func TestAGiantOwedEpisodeStillWillNotOverrunABookedShow(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 20, 0, 0, time.UTC)

	giant := podcastSource("pod-wan", "WAN Show", "pwan")
	short := podcastSource("pod-short", "Filler", "pshort")
	fill := []catalog.PodcastEpisode{}
	for index := 0; index < 12; index++ {
		fill = append(fill, episode("f"+strconv.Itoa(index), "Filler "+strconv.Itoa(index),
			now.AddDate(0, 0, -60-index), 30))
	}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		// Owed, 239 minutes — the real one from tonight.
		"pwan":   {episode("wan", "My Family Left Me - WAN Show", now.Add(-9*time.Hour), 239)},
		"pshort": fill,
	}}

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks: []Block{
			{ID: "fresh", Default: true,
				Pools:   []PoolRef{{Pool: "talk"}},
				Pattern: []PatternStep{{Want: WantObligation}}},
			{ID: "booked", Label: "Coast to Coast",
				Enter: BlockEntry{At: "22:00", Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "23:00"},
				Pools: []PoolRef{{Pool: "talk"}}},
		},
	}
	s := newStation(t, plan, []Source{giant, short}, cat, now)

	item, decision := s.decide()
	if item.ItemRef == "episode:wan" {
		t.Fatalf("started a 239-minute episode 99 minutes before a booked show\n%s",
			decision.Explain())
	}
	// And the record must say why, rather than just that it did not.
	said := false
	for _, rejection := range decision.Rejected {
		if rejection.Ref == "episode:wan" {
			said = true
		}
	}
	if !said {
		t.Fatalf("the record does not explain why the owed episode could not air\n%s",
			decision.Explain())
	}
}
