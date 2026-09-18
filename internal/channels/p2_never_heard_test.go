package channels

import (
	"context"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// From Jake's station, the decision records of 2026-09-16 evening and
// 2026-09-17, read off the live box. The plan surfaces S-tier and A-tier
// episodes twice, and the queue ordered what it owed by tier first — so an
// episode the station had already aired once, still owed its second hearing,
// sat above every episode nobody had heard at all:
//
//	19:03  played  Ep 636 - Mr. Chili's (feat. Sam Tallent)   S  credit 1 of 2
//	       over    Tylenol In Pregnancy Linked To …            B  credit 0
//	               Improve Flexibility with Research-Supp…     B  credit 0
//	20:21  played  Ghost Brothers (feat. Myles Johnson) …      A  credit 1 of 2
//	       over    the same two, still never heard
//	12:15  played  205: Superstar                              A  credit 1 of 2
//	       over    the same two and #2555 - Ron White          B  credit 0
//
// Every one of those was "highest scoring candidate": urgency had no term for
// never having been heard, the freshness term carried the S-tier episode to
// the top of the score, and the contender band then narrowed to it. The rule
// wanted is an order, not a weight — an episode nobody has heard beats one
// somebody has, across every tier, and a second surfacing airs only when
// nothing unheard can. These reproduce the three records by name.

// neverHeardStation is Jake's plan shape with the shows from the records: the
// general block, and a new-episodes block whose cycle alternates a music
// break with an obligation position.
func neverHeardStation(t *testing.T, now time.Time) *station {
	t.Helper()
	plan := Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 1}, {ID: "music", Target: 0}},
		ListeningDay: &DaySpec{Start: "08:00", End: "23:00"},
		Freshness:    FreshnessPolicy{Surfacings: map[string]int{"S": 2, "A": 2}},
		LongForm:     LongFormPolicy{Threshold: "2h", Rest: "21d"},
		Horizons:     Horizons{Recency: "14d"},
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

	show := func(id, label, tier string) Source {
		src := podcastSource(id, label, "p-"+id)
		src.Config["tier"] = tier
		return src
	}
	sources := []Source{
		show("mssp", "Matt and Shane's Secret Podcast", "S"),
		show("ridley", "Radio Ridley Radio with Michael Ridley", "A"),
		show("harland", "The Harland Highway", "A"),
		show("church", "The Church of What's Happening Now", "A"),
		show("lemonparty", "lemonparty", "A"),
		show("drew", "Ask Dr. Drew", "B"),
		show("huberman", "Huberman Lab", "B"),
		show("jre", "The Joe Rogan Experience", "B"),
		musicSource("mus1", "Explore", "pl1"),
	}
	day := func(offset int, hour, minute int) time.Time {
		return time.Date(2026, 9, 16+offset, hour, minute, 0, 0, time.UTC)
	}
	episodes := map[string][]catalog.PodcastEpisode{
		"p-mssp":       {episode("ep636", "Ep 636 - Mr. Chili's (feat. Sam Tallent)", day(0, 6, 0), 75)},
		"p-ridley":     {episode("ghost", "Ghost Brothers (feat. Myles Johnson) - Radio Ridley Radio | Ep. 151", day(0, 6, 0), 68)},
		"p-harland":    {episode("matan", "MATAN introduces SPIDER-GIRL to the world! Also golden gopher teeth, spring rolls, and old age!", day(-1, 9, 0), 77)},
		"p-church":     {episode("chapter", "The beginning of a new chapter", day(-1, 6, 30), 76)},
		"p-lemonparty": {episode("superstar", "205: Superstar", day(-1, 19, 0), 78)},
		"p-drew":       {episode("tylenol", "Tylenol In Pregnancy Linked To Female Reproductive Issues", day(0, 11, 30), 67)},
		"p-huberman":   {episode("flex", "Improve Flexibility with Research-Supported Stretching Protocols", day(0, 11, 0), 126)},
		"p-jre":        {episode("ronwhite", "#2555 - Ron White", day(0, 17, 22), 154)},
	}
	// A shelf of back catalogue behind every show, as the real station has,
	// so nothing here is decided by an empty rotation.
	for _, id := range []string{"mssp", "ridley", "harland", "church", "lemonparty", "drew", "huberman", "jre"} {
		for back := 1; back <= 6; back++ {
			episodes["p-"+id] = append(episodes["p-"+id], episode(id+"-old"+strconv.Itoa(back),
				id+" archive "+strconv.Itoa(back), now.AddDate(0, 0, -20-back), 60))
		}
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 60; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%20), 200))
	}
	return newStation(t, plan, sources,
		&stubCatalog{episodes: episodes, playlists: map[string][]catalog.MusicTrack{"pl1": songs}}, now)
}

// heardOnce is the station having aired an episode in full, earlier: the play
// log has the airing and the obligation carries its credit, exactly as the
// streamer leaves things. The clock is not moved.
func (s *station) heardOnce(sourceID, episodeID string, length time.Duration, at time.Time) {
	s.t.Helper()
	ref := "episode:" + episodeID
	s.history.Record(MemoryPlay{
		SourceID: sourceID, ItemRef: ref, Category: "talk",
		StartedAt: at, EndedAt: at.Add(length),
		DurationSeconds: int(length / time.Second),
	})
	if err := s.engine.Obligations.Credit(context.Background(), ref, 1, at.Add(length)); err != nil {
		s.t.Fatalf("credit: %v", err)
	}
}

// atObligationPosition puts the station in the new-episodes block at the
// position that asks for something owed, the way the three records were.
func (s *station) atObligationPosition(entered time.Time) {
	s.state = ProgramState{BlockID: "fresh", EnteredAt: entered, PatternIndex: 1, LastWasBreak: true, ItemCount: 1}
}

// creditOf is what the obligation store says an episode has earned.
func (s *station) creditOf(episodeID string) float64 {
	s.t.Helper()
	stored, err := s.engine.Obligations.List(context.Background(), s.now)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, obligation := range stored {
		if obligation.ItemRef == "episode:"+episodeID {
			return obligation.Credit
		}
	}
	s.t.Fatalf("%s is not an obligation at all; the fixture is not testing what it says", episodeID)
	return 0
}

// The 19:03 record. Matt and Shane's Ep 636 — S tier, aired in full at 08:08,
// owed a second surfacing — went out over two B-tier episodes nobody had
// heard. A brand-new B-tier episode beats a heard-once S-tier one.
func TestANeverHeardEpisodeBeatsAHeardOnceEpisodeOfAHigherTier(t *testing.T) {
	now := time.Date(2026, 9, 16, 19, 3, 31, 0, time.UTC)
	s := neverHeardStation(t, now)
	s.env() // notice today's episodes, as the morning's decisions did
	s.heardOnce("mssp", "ep636", 75*time.Minute, time.Date(2026, 9, 16, 8, 8, 0, 0, time.UTC))
	s.heardOnce("harland", "matan", 77*time.Minute, time.Date(2026, 9, 15, 14, 0, 0, 0, time.UTC))
	s.heardOnce("church", "chapter", 76*time.Minute, time.Date(2026, 9, 15, 15, 30, 0, 0, time.UTC))
	s.heardOnce("ridley", "ghost", 68*time.Minute, time.Date(2026, 9, 16, 11, 22, 0, 0, time.UTC))
	s.heardOnce("lemonparty", "superstar", 78*time.Minute, time.Date(2026, 9, 16, 15, 9, 0, 0, time.UTC))
	// JRE's Ron White was published at 17:22 and is not in this record's shelf
	// (it had not been noticed yet); the 12:15 record below has it.
	s.engine.Catalog.(*stubCatalog).episodes["p-jre"] = s.engine.Catalog.(*stubCatalog).episodes["p-jre"][1:]

	if credit := s.creditOf("ep636"); credit != 1 {
		t.Fatalf("Ep 636 should be heard once and still owed, credit %v", credit)
	}
	if !s.env().owed.Owes("episode:ep636") {
		t.Fatal("one airing of two settled Ep 636; the fixture is not testing a second surfacing")
	}
	s.atObligationPosition(now.Add(-10 * time.Hour))

	item, decision := s.decide()
	if rule, reason := rejectionOf(decision, "episode:tylenol"); rule != "" {
		t.Fatalf("the never-heard B-tier episode was refused (%s: %s), so the fixture is not testing the order\n%s",
			rule, reason, decision.Explain())
	}
	if item.ItemRef == "episode:ep636" {
		t.Fatalf("a heard-once S-tier episode went out over two never-heard B-tier ones\n%s", decision.Explain())
	}
	if item.ItemRef != "episode:tylenol" {
		t.Fatalf("played %q, want the newest never-heard episode (Tylenol In Pregnancy…)\n%s", item.Title, decision.Explain())
	}
	if decision.Selected == nil || !decision.Selected.Owed || decision.Selected.Reason != "most urgent of what is owed" {
		t.Fatalf("the record should say the queue decided, got %+v", decision.Selected)
	}
}

// The 20:21 record, an hour and a quarter on: Ghost Brothers — A tier, aired
// in full that morning — over the same two never-heard B-tier episodes. The
// order holds one tier down as well: heard once is heard once.
func TestANeverHeardEpisodeBeatsAHeardOnceEpisodeOfTheNextTierToo(t *testing.T) {
	now := time.Date(2026, 9, 16, 20, 21, 59, 0, time.UTC)
	s := neverHeardStation(t, now)
	s.env()
	// Ep 636 has now had both its surfacings, as it had by 20:21.
	s.heardOnce("mssp", "ep636", 75*time.Minute, time.Date(2026, 9, 16, 8, 8, 0, 0, time.UTC))
	s.heardOnce("mssp", "ep636", 75*time.Minute, time.Date(2026, 9, 16, 19, 3, 0, 0, time.UTC))
	s.heardOnce("ridley", "ghost", 68*time.Minute, time.Date(2026, 9, 16, 8, 40, 0, 0, time.UTC))
	s.heardOnce("harland", "matan", 77*time.Minute, time.Date(2026, 9, 15, 14, 0, 0, 0, time.UTC))
	s.heardOnce("church", "chapter", 76*time.Minute, time.Date(2026, 9, 15, 15, 30, 0, 0, time.UTC))
	s.heardOnce("lemonparty", "superstar", 78*time.Minute, time.Date(2026, 9, 16, 15, 9, 0, 0, time.UTC))
	s.engine.Catalog.(*stubCatalog).episodes["p-jre"] = s.engine.Catalog.(*stubCatalog).episodes["p-jre"][1:]

	if s.env().owed.Owes("episode:ep636") {
		t.Fatal("two airings of two left Ep 636 owed")
	}
	if credit := s.creditOf("ghost"); credit != 1 {
		t.Fatalf("Ghost Brothers should be heard once and still owed, credit %v", credit)
	}
	s.atObligationPosition(now.Add(-11 * time.Hour))

	item, decision := s.decide()
	if rule, reason := rejectionOf(decision, "episode:tylenol"); rule != "" {
		t.Fatalf("the never-heard B-tier episode was refused (%s: %s), so the fixture is not testing the order\n%s",
			rule, reason, decision.Explain())
	}
	for _, heard := range []string{"episode:ghost", "episode:matan", "episode:chapter"} {
		if item.ItemRef == heard {
			t.Fatalf("a heard-once A-tier episode went out over two never-heard B-tier ones\n%s", decision.Explain())
		}
	}
	if item.ItemRef != "episode:tylenol" {
		t.Fatalf("played %q, want the newest never-heard episode\n%s", item.Title, decision.Explain())
	}
}

// The 12:15 record the next day: 205: Superstar, aired the previous afternoon,
// over three never-heard B-tier episodes — one of them a two-and-a-half-hour
// JRE that the commitment cost had put well outside the contender band, at
// 2.81 against a top of 4.02. The band is for choosing between interchangeable
// records; the queue's order does not depend on the score, so a never-heard
// giant that fits the room still goes before a second surfacing however the
// score ranked it — and before a newer-scoring never-heard episode of its own
// tier, because it is the newest of them.
//
// The record's band excluded the giant by a commitment cost this fixture's
// shelf cannot reproduce (its typical item is longer), so the plan's own dial
// is turned down until the band excludes it the same way. That is a setting
// any plan can carry, and the order has to hold under it.
func TestTheQueuesOrderDoesNotDependOnTheScoreBand(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 15, 45, 0, time.UTC)
	s := neverHeardStation(t, now)
	s.engine.Plan.Selection.Epsilon = 0.02
	s.env()
	s.heardOnce("mssp", "ep636", 75*time.Minute, time.Date(2026, 9, 16, 8, 8, 0, 0, time.UTC))
	s.heardOnce("mssp", "ep636", 75*time.Minute, time.Date(2026, 9, 16, 19, 3, 0, 0, time.UTC))
	s.heardOnce("ridley", "ghost", 68*time.Minute, time.Date(2026, 9, 16, 8, 40, 0, 0, time.UTC))
	s.heardOnce("ridley", "ghost", 68*time.Minute, time.Date(2026, 9, 16, 20, 22, 0, 0, time.UTC))
	s.heardOnce("harland", "matan", 77*time.Minute, time.Date(2026, 9, 15, 14, 0, 0, 0, time.UTC))
	s.heardOnce("church", "chapter", 76*time.Minute, time.Date(2026, 9, 15, 15, 30, 0, 0, time.UTC))
	s.heardOnce("lemonparty", "superstar", 78*time.Minute, time.Date(2026, 9, 16, 15, 9, 0, 0, time.UTC))
	s.atObligationPosition(now.Add(-4 * time.Hour))

	item, decision := s.decide()
	var giant *CandidateSummary
	for index := range decision.Candidates {
		if decision.Candidates[index].Ref == "episode:ronwhite" {
			giant = &decision.Candidates[index]
		}
	}
	if giant == nil {
		t.Fatalf("Ron White was not scored at all, so the fixture is not testing the band\n%s", decision.Explain())
	}
	if top := decision.Candidates[0]; top.Ref == "episode:ronwhite" || giant.Score >= top.Score*(1-s.engine.Plan.epsilon()) {
		t.Fatalf("Ron White scored %.2f against a top of %.2f — inside the band, so the fixture is not testing it\n%s",
			giant.Score, top.Score, decision.Explain())
	}
	if item.ItemRef != "episode:ronwhite" {
		t.Fatalf("played %q, want the newest never-heard episode whatever its score\n%s", item.Title, decision.Explain())
	}
	if !giant.Contender {
		t.Fatalf("the record does not mark the chosen episode as the contender\n%s", decision.Explain())
	}
}

// A second surfacing still goes out — when nothing unheard can. Everything
// never heard is too long for the room before the booked show; the heard-once
// S-tier episode, its separation run, is the most urgent thing that fits.
func TestASecondSurfacingAirsWhenNothingUnheardCan(t *testing.T) {
	now := time.Date(2026, 9, 16, 20, 21, 59, 0, time.UTC)
	s := neverHeardStation(t, now)
	// Nothing is booked in the fixture; the room is the play window handed to
	// the decision, so give this position exactly an hour.
	s.engine.Plan.Blocks = append(s.engine.Plan.Blocks, Block{
		ID: "slot-news", Label: "News",
		Enter: BlockEntry{At: "21:22", Days: "*", Hard: true, Start: StartImmediately},
		Exit:  BlockExit{At: "22:00"},
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
	// The S-tier episode is 75 minutes; make it the one thing owed that fits.
	s.engine.Catalog.(*stubCatalog).episodes["p-mssp"][0].DurationSeconds = 50 * 60
	s.env()
	s.heardOnce("mssp", "ep636", 50*time.Minute, time.Date(2026, 9, 16, 8, 8, 0, 0, time.UTC))
	s.heardOnce("ridley", "ghost", 68*time.Minute, time.Date(2026, 9, 16, 8, 40, 0, 0, time.UTC))
	s.heardOnce("harland", "matan", 77*time.Minute, time.Date(2026, 9, 15, 14, 0, 0, 0, time.UTC))
	s.heardOnce("church", "chapter", 76*time.Minute, time.Date(2026, 9, 15, 15, 30, 0, 0, time.UTC))
	s.heardOnce("lemonparty", "superstar", 78*time.Minute, time.Date(2026, 9, 16, 15, 9, 0, 0, time.UTC))
	s.atObligationPosition(now.Add(-11 * time.Hour))

	item, decision := s.decide()
	for _, unheard := range []string{"episode:tylenol", "episode:flex", "episode:ronwhite"} {
		if rule, _ := rejectionOf(decision, unheard); rule != ruleFitsBeforeAnchor {
			t.Fatalf("%s should be held by the booked show, got %q\n%s", unheard, rule, decision.Explain())
		}
	}
	if item.ItemRef != "episode:ep636" {
		t.Fatalf("with nothing unheard able to air, the S-tier second surfacing should go out, got %q\n%s",
			item.Title, decision.Explain())
	}
}

// The threshold itself: heard is more than half of it reaching the listener.
// The 52 of 87 minutes of Comedy Bang Bang from 2026-08-11 is heard; the
// seven fifteen-second false starts of a Theo Von episode that morning are
// not; an airing into a block worth nothing is not, however long it ran.
func TestHeardIsMoreThanHalfOfTheEpisodeReachingTheListener(t *testing.T) {
	cases := []struct {
		name   string
		credit float64
		heard  bool
	}{
		{"never aired", 0, false},
		{"a fifteen-second false start of an hour", 15.0 / 3600, false},
		{"the whole episode into a block worth nothing", 0, false},
		{"five of forty-five minutes", 5.0 / 45, false},
		{"exactly half", 0.5, false},
		{"52 of 87 minutes", 52.0 / 87, true},
		{"aired in full", 1, true},
		{"aired in full, then a false start", 1.004, true},
	}
	for _, tc := range cases {
		if got := (Obligation{Credit: tc.credit}).Heard(); got != tc.heard {
			t.Errorf("%s (credit %.3f): heard = %v, want %v", tc.name, tc.credit, got, tc.heard)
		}
	}
	if heardThreshold != 0.5 {
		t.Fatalf("the documented threshold is half; the constant says %v", heardThreshold)
	}
}

// Never heard is a class, not a bonus: no tier, age or deadline carries a
// heard episode past an unheard one, under the default weights or under a
// plan that has stretched every one of them.
func TestNeverHeardOutranksHeardUnderAnyWeights(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	// The strongest heard-once claim there is: S tier, brand new, and about
	// to expire all at once (a window so short both terms are near their max).
	heardS := Obligation{ItemRef: "s", Tier: TierS, Credit: 1, SettleAt: 2, State: ObligationPending,
		PublishedAt: now.Add(-9 * time.Hour), ExpiresAt: now.Add(1 * time.Hour)}
	// The weakest unheard claim: bottom tier, old, and with most of its window
	// still to run so nothing lifts it.
	unheardF := Obligation{ItemRef: "f", Tier: TierF, Credit: 0.5, SettleAt: 1, State: ObligationPending,
		PublishedAt: now.Add(-70 * time.Hour), ExpiresAt: now.Add(300 * time.Hour)}
	for _, policy := range []FreshnessPolicy{
		{},
		{TierSpread: 10, RecencyWeight: 4, ExpiryWeight: 12, UrgentFrom: 0.5},
		{TierSpread: 0.5, RecencyWeight: 3, ExpiryWeight: 3},
	} {
		queue := NewObligationQueue([]Obligation{heardS, unheardF}, now, policy)
		if queue.Pending[0].ItemRef != "f" {
			t.Fatalf("under %+v a heard-once S-tier episode (%.2f) outranked a never-heard F-tier one (%.2f)",
				policy, heardS.Urgency(now, policy), unheardF.Urgency(now, policy))
		}
	}
	// And within a class the tiers still order as they always did.
	unheardA := Obligation{ItemRef: "a", Tier: TierA, SettleAt: 2, State: ObligationPending,
		PublishedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(70 * time.Hour)}
	heardA := Obligation{ItemRef: "ha", Tier: TierA, Credit: 1, SettleAt: 2, State: ObligationPending,
		PublishedAt: now.Add(-1 * time.Hour), ExpiresAt: now.Add(71 * time.Hour)}
	queue := NewObligationQueue([]Obligation{heardS, unheardF, unheardA, heardA}, now, FreshnessPolicy{})
	want := []string{"a", "f", "s", "ha"}
	for index, ref := range want {
		if queue.Pending[index].ItemRef != ref {
			got := []string{}
			for _, o := range queue.Pending {
				got = append(got, o.ItemRef)
			}
			t.Fatalf("queue is %v, want never-heard by tier then heard by tier: %v", got, want)
		}
	}
}

// The choice, on its own. When the top scorer is owed, the most urgent owed
// candidate of its category plays: outside the band, with no randomness, and
// never from another category.
func TestTheMostUrgentOwedCandidateWinsWhateverTheBandSays(t *testing.T) {
	heardS := ScoredCandidate{Candidate: Candidate{Ref: "s", Category: "talk", Owed: true, Urgency: 12.9}, Total: 4.3}
	unheardB := ScoredCandidate{Candidate: Candidate{Ref: "b", Category: "talk", Owed: true, Urgency: 25.3}, Total: 2.8}
	unheardNews := ScoredCandidate{Candidate: Candidate{Ref: "n", Category: "news", Owed: true, Urgency: 29.5}, Total: 4.2}
	rerun := ScoredCandidate{Candidate: Candidate{Ref: "old", Category: "talk"}, Total: 4.1}

	chosen, contenders := chooseCandidate([]ScoredCandidate{heardS, rerun, unheardB}, 0.15, rand.New(rand.NewSource(1)))
	if chosen.Candidate.Ref != "b" || len(contenders) != 1 {
		t.Fatalf("chose %s among %d, want the never-heard episode outside the band", chosen.Candidate.Ref, len(contenders))
	}
	// The plan asking for no randomness at all does not switch the order off.
	chosen, _ = chooseCandidate([]ScoredCandidate{heardS, rerun, unheardB}, 0, nil)
	if chosen.Candidate.Ref != "b" {
		t.Fatalf("with epsilon 0 the top scorer %s played over the more urgent owed episode", chosen.Candidate.Ref)
	}
	// Which category plays is the balance's call: a more urgent episode in
	// another category does not reach across.
	chosen, _ = chooseCandidate([]ScoredCandidate{heardS, unheardNews, unheardB}, 0.15, nil)
	if chosen.Candidate.Ref != "b" {
		t.Fatalf("chose %s, want the most urgent owed episode of the winning category", chosen.Candidate.Ref)
	}
	// And when the balance chose something not owed, the queue has no say.
	song := ScoredCandidate{Candidate: Candidate{Ref: "song", Category: "music"}, Total: 4.5}
	chosen, _ = chooseCandidate([]ScoredCandidate{song, heardS, unheardB}, 0.01, nil)
	if chosen.Candidate.Ref != "song" {
		t.Fatalf("chose %s over the song the balance picked", chosen.Candidate.Ref)
	}
}

// ---- the two incidents as filed: 2026-09-15 -----------------------------
//
// The report named Stavvy's World and Comedy Bang Bang; the records for those
// are two days earlier than the ones above (docs/SCHEDULER_LOGIC.md §11), and
// they are the same defect with the same arithmetic. Reproduced here by name,
// at the obligation position of the new-episodes block — the 08:59 record was
// the general block taking over a released music hour, which is the boundary
// path and P3's fixture; the ordering that chose the episode is the same one.

// incidentStation is the 2026-09-15 shelf: Stavvy's #198 aired in full the
// afternoon before, Comedy Bang Bang's Male Loneliness Empanada likewise, and
// three A-tier episodes plus a B-tier one published overnight and never heard.
func incidentStation(t *testing.T, now time.Time) *station {
	t.Helper()
	s := neverHeardStation(t, now)
	cat := s.engine.Catalog.(*stubCatalog)
	stavvy := podcastSource("stavvy", "Stavvy's World", "p-stavvy")
	stavvy.Config["tier"] = "S"
	cartalk := podcastSource("cartalk", "The Best of Car Talk", "p-cartalk")
	cartalk.Config["tier"] = "A"
	cbb := podcastSource("cbb", "Comedy Bang Bang: The Podcast", "p-cbb")
	cbb.Config["tier"] = "A"
	s.engine.Sources = append(s.engine.Sources, stavvy, cartalk, cbb)
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 9, day, hour, minute, 0, 0, time.UTC) }
	cat.episodes["p-stavvy"] = []catalog.PodcastEpisode{episode("nikki", "#198 - Nikki Glaser and JP McDade", at(14, 4, 0), 102)}
	cat.episodes["p-cartalk"] = []catalog.PodcastEpisode{episode("empathic", "#2674: Empathic Whatever", at(15, 1, 0), 34)}
	cat.episodes["p-cbb"] = []catalog.PodcastEpisode{episode("empanada", "Male Loneliness Empanada (Mark Duplass, Devin Field, Lily Sullivan)", at(14, 0, 5), 93)}
	cat.episodes["p-harland"] = []catalog.PodcastEpisode{episode("matan", "MATAN introduces SPIDER-GIRL to the world! Also golden gopher teeth, spring rolls, and old age!", at(15, 3, 0), 77)}
	cat.episodes["p-church"] = []catalog.PodcastEpisode{episode("chapter", "The beginning of a new chapter", at(15, 0, 34), 76)}
	cat.episodes["p-drew"] = []catalog.PodcastEpisode{episode("feminism", "Feminism On Trial: Lindsay Clancy Puts All Women On The Stand", at(15, 4, 59), 97)}
	cat.episodes["p-jre"] = []catalog.PodcastEpisode{episode("rovelli", "#2554 - Carlo Rovelli", at(15, 11, 0), 158)}
	// Nothing owed from the shows the 09-16 shelf carried.
	cat.episodes["p-mssp"], cat.episodes["p-ridley"], cat.episodes["p-lemonparty"], cat.episodes["p-huberman"] = nil, nil, nil, nil
	for _, id := range []string{"stavvy", "cartalk", "cbb"} {
		for back := 1; back <= 6; back++ {
			cat.episodes["p-"+id] = append(cat.episodes["p-"+id], episode(id+"-old"+strconv.Itoa(back),
				id+" archive "+strconv.Itoa(back), now.AddDate(0, 0, -20-back), 60))
		}
	}
	return s
}

// Incident B, 2026-09-15 08:59:23: #198 - Nikki Glaser and JP McDade — S
// tier, aired in full 11:56–13:38 the day before, credit 1 of 2 — went out
// over MATAN, #2674: Empathic Whatever and The beginning of a new chapter,
// all A tier, all published overnight, none heard. Reason: "highest scoring
// candidate", freshness 1.000 against 0.87/0.86/0.86. A never-heard A-tier
// episode beats a heard-once S-tier one, and the newest of them goes first.
func TestIncidentBStavvysSecondSurfacingWaitsBehindThreeNewATierEpisodes(t *testing.T) {
	now := time.Date(2026, 9, 15, 8, 59, 23, 0, time.UTC)
	s := incidentStation(t, now)
	s.env()
	s.heardOnce("stavvy", "nikki", 102*time.Minute, time.Date(2026, 9, 14, 11, 56, 0, 0, time.UTC))
	s.heardOnce("cbb", "empanada", 93*time.Minute, time.Date(2026, 9, 14, 14, 30, 0, 0, time.UTC))
	if credit := s.creditOf("nikki"); credit != 1 {
		t.Fatalf("#198 should be heard once and still owed, credit %v", credit)
	}
	s.atObligationPosition(now.Add(-59 * time.Minute))

	item, decision := s.decide()
	for _, unheard := range []string{"episode:matan", "episode:empathic", "episode:chapter"} {
		if rule, reason := rejectionOf(decision, unheard); rule != "" {
			t.Fatalf("%s was refused (%s: %s), so the fixture is not testing the order\n%s", unheard, rule, reason, decision.Explain())
		}
	}
	if item.ItemRef == "episode:nikki" {
		t.Fatalf("Stavvy's second surfacing went out over three never-heard A-tier episodes\n%s", decision.Explain())
	}
	if item.ItemRef != "episode:matan" {
		t.Fatalf("played %q, want the newest never-heard A-tier episode (MATAN)\n%s", item.Title, decision.Explain())
	}
}

// Incident A, 2026-09-15 12:25:56: Male Loneliness Empanada — Comedy Bang
// Bang, A tier, aired in full the day before, credit 1 of 2 — went out over
// Feminism On Trial (Ask Dr. Drew, B, 97 m, never heard) and #2554 - Carlo
// Rovelli (JRE, B, 158 m, published 11:00, never heard), every other A-tier
// episode being held by a separation rule. A never-heard B-tier episode beats
// a heard-once A-tier one; among the two, the newer goes first.
func TestIncidentAComedyBangBangsSecondSurfacingWaitsBehindNewBTierEpisodes(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 25, 56, 0, time.UTC)
	s := incidentStation(t, now)
	s.env()
	s.heardOnce("stavvy", "nikki", 102*time.Minute, time.Date(2026, 9, 14, 11, 56, 0, 0, time.UTC))
	s.heardOnce("cbb", "empanada", 93*time.Minute, time.Date(2026, 9, 14, 14, 30, 0, 0, time.UTC))
	// The morning as the record had it: #198's fifteen-second false start at
	// 08:59, then MATAN, Empathic Whatever and The beginning of a new chapter
	// each aired in full, and a two-song break to 12:25.
	s.history.Record(MemoryPlay{SourceID: "stavvy", ItemRef: "episode:nikki", Category: "talk",
		StartedAt: time.Date(2026, 9, 15, 8, 59, 23, 0, time.UTC), EndedAt: time.Date(2026, 9, 15, 8, 59, 38, 0, time.UTC), DurationSeconds: 15})
	if err := s.engine.Obligations.Credit(context.Background(), "episode:nikki", 15.0/(102*60), now); err != nil {
		t.Fatal(err)
	}
	s.heardOnce("harland", "matan", 77*time.Minute, time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC))
	s.heardOnce("cartalk", "empathic", 34*time.Minute, time.Date(2026, 9, 15, 10, 22, 0, 0, time.UTC))
	s.heardOnce("church", "chapter", 76*time.Minute, time.Date(2026, 9, 15, 11, 4, 0, 0, time.UTC))
	for index, start := range []time.Time{time.Date(2026, 9, 15, 12, 20, 7, 0, time.UTC), time.Date(2026, 9, 15, 12, 23, 2, 0, time.UTC)} {
		s.history.Record(MemoryPlay{SourceID: "mus1", ItemRef: "track:t" + strconv.Itoa(index), Category: "music",
			StartedAt: start, EndedAt: start.Add(3 * time.Minute), DurationSeconds: 180})
	}
	s.atObligationPosition(time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC))

	item, decision := s.decide()
	if rule, _ := rejectionOf(decision, "episode:nikki"); rule != "itemSeparation" {
		t.Fatalf("#198 should be held by item separation after its false start, got %q\n%s", rule, decision.Explain())
	}
	for _, unheard := range []string{"episode:feminism", "episode:rovelli"} {
		if rule, reason := rejectionOf(decision, unheard); rule != "" {
			t.Fatalf("%s was refused (%s: %s), so the fixture is not testing the order\n%s", unheard, rule, reason, decision.Explain())
		}
	}
	if item.ItemRef == "episode:empanada" {
		t.Fatalf("Comedy Bang Bang's second surfacing went out over two never-heard B-tier episodes\n%s", decision.Explain())
	}
	if item.ItemRef != "episode:rovelli" {
		t.Fatalf("played %q, want the newest never-heard episode (#2554 - Carlo Rovelli)\n%s", item.Title, decision.Explain())
	}
}
