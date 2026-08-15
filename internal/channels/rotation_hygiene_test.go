package channels

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// A booked show is not rotation inventory.
//
// Every scheduled hour on a real station is backed by its own source — the news
// feed, the overnight lofi stream, the shortwave relay — and those sources are
// spoken word, so a pool that matches "category: talk" happily swept all of
// them into the general rotation. The station then played the news stream at
// half past two in the afternoon as though it were a podcast.
func TestMatchPoolsLeaveBookedShowsAlone(t *testing.T) {
	show := Source{ID: "krcc", Kind: SourceLiveStream, Label: "KRCC", Enabled: true, Role: RoleShow}
	podcast := podcastSource("pod1", "A Podcast", "p1")

	rotation := Pool{ID: "talk", Match: &PoolMatch{Category: LegacyCategoryTalk}}
	if rotation.Selects(show) {
		t.Fatalf("a match pool selected a booked show — it would turn up at random in the rotation")
	}
	if !rotation.Selects(podcast) {
		t.Fatalf("a match pool must still select ordinary rotation content")
	}

	// The anchored block reaches its own show by NAMING it, and that must keep
	// working or the booked hour has nothing to play.
	booked := Pool{ID: "news", SourceIDs: []string{"krcc"}}
	if !booked.Selects(show) {
		t.Fatalf("naming a show in a pool must still select it")
	}

	// And a pool that deliberately asks for shows still gets them.
	explicit := Pool{ID: "shows", Match: &PoolMatch{Role: RoleShow}}
	if !explicit.Selects(show) {
		t.Fatalf("a pool that explicitly matches role=show must select shows")
	}
}

// A block whose entry is a CONDITION rather than a clock time.
//
// "While the station still owes you episodes" is a mode, not a daypart. Block
// resolution was keyed entirely on Enter.At, so a plan could express the
// condition, save it, validate it and display it — and the block could never be
// entered. The station simply stayed in its default rotation for ever, with no
// error anywhere.
func TestAConditionalBlockCanActuallyBeEntered(t *testing.T) {
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks: []Block{
			{ID: "archive", Label: "Archive rotation", Default: true,
				Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "fresh", Label: "New episodes",
				Enter: BlockEntry{When: "obligations.pending > 0"},
				Exit:  BlockExit{When: "obligations.pending == 0"},
				Next:  "archive",
				Pools: []PoolRef{{Pool: "talk"}}},
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("a condition-entered block should be a valid plan: %v", err)
	}
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)

	owed := ConditionContext{ObligationsPending: 3}
	if got := ResolveBlock(plan, Timeline{}, ProgramState{}, owed, now); got.Block.ID != "fresh" {
		t.Fatalf("with three episodes owed the station should be in %q, not %q", "fresh", got.Block.ID)
	}

	settled := ConditionContext{ObligationsPending: 0}
	if got := ResolveBlock(plan, Timeline{}, ProgramState{}, settled, now); got.Block.ID != "archive" {
		t.Fatalf("with nothing owed the station should fall back to %q, not %q", "archive", got.Block.ID)
	}

	// And it must hand back once the queue empties, rather than sitting in a
	// mode whose condition stopped being true.
	inFresh := ProgramState{BlockID: "fresh", EnteredAt: now.Add(-time.Hour)}
	if got := ResolveBlock(plan, Timeline{}, inFresh, settled, now); got.Block.ID != "archive" {
		t.Fatalf("the fresh block should exit when nothing is owed, got %q", got.Block.ID)
	}
}

// The same show added twice must rest as ONE show.
//
// Jacob has Hardcore History twice over: the episodes sitting on disk and the
// same show's RSS feed, added as two sources so both are reachable. Rationing
// keyed on the source rested one and left the other completely eligible, so
// "a giant rests for a week" did nothing whatsoever.
func TestAGiantRestsAcrossEveryCopyOfTheShow(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	// One podcast, two sources — exactly the on-disk / RSS pair.
	fromFeed := podcastSource("hh-feed", "", "phh")
	fromDisk := podcastSource("hh-disk", "", "phh")
	if ShowOf(fromFeed) != ShowOf(fromDisk) {
		t.Fatalf("two sources for one podcast must be one show: %q vs %q",
			ShowOf(fromFeed), ShowOf(fromDisk))
	}

	filler := podcastSource("pod-filler", "Filler", "pfill")
	fill := []catalog.PodcastEpisode{}
	for index := 0; index < 20; index++ {
		fill = append(fill, episode("f"+strconv.Itoa(index), "Filler "+strconv.Itoa(index),
			now.AddDate(0, 0, -40-index), 30))
	}

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
		LongForm:   LongFormPolicy{Threshold: "2h", Rest: "7d"},
	}
	// Two giants, so this is about the SHOW resting and not merely about the
	// same episode being repeated — item separation already covers that.
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"phh": {
			episode("hh-1", "Mania for Subjugation I", now.AddDate(0, 0, -300), 240),
			episode("hh-2", "Mania for Subjugation II", now.AddDate(0, 0, -270), 250),
		},
		"pfill": fill,
	}}
	s := newStation(t, plan, []Source{fromFeed, fromDisk, filler}, cat, now)

	// The giant went out an hour ago, from the FEED copy.
	s.history.Record(MemoryPlay{
		SourceID: "hh-feed", ItemRef: "episode:hh-1", Category: "talk",
		StartedAt: now.Add(-5 * time.Hour), EndedAt: now.Add(-time.Hour),
		DurationSeconds: 240 * 60,
	})

	item, decision := s.decide()

	// The OTHER giant, reached through the OTHER source, must be rested too.
	rested := false
	for _, rejection := range decision.Rejected {
		if rejection.Ref == "episode:hh-2" && rejection.Rule == "longFormRationing" {
			rested = true
		}
	}
	if !rested {
		t.Fatalf("a second giant from the same show, reached through its other source, was not rested:\n%s",
			decision.Explain())
	}
	if item.SourceID == "hh-feed" || item.SourceID == "hh-disk" {
		t.Fatalf("played %q — the show aired an hour ago and should be resting", item.Title)
	}
}

// The status panel must resolve the block against the same world the scheduler
// does.
//
// The peek built its condition context without the obligation count, so every
// block gated on "while episodes are owed" reported as not entered however many
// were owed — the panel confidently named the wrong block while the station
// played the right one. A status screen that disagrees with the scheduler is
// worse than none: it is the thing you check to find out why the scheduler is
// behaving oddly.
func TestTheStatusPeekKnowsWhatIsOwed(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 10, 0, 0, time.UTC)

	show := podcastSource("pod-new", "A Show", "pnew")
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"pnew": {episode("ep-today", "Today's episode", now.Add(-6*time.Hour), 40)},
	}}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks: []Block{
			{ID: "general", Label: "General rotation", Default: true,
				Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "fresh", Label: "New episodes",
				Enter: BlockEntry{At: "08:00", Days: "*", When: "obligations.pending > 0"},
				Exit:  BlockExit{When: "obligations.pending == 0"},
				Next:  "general",
				Pools: []PoolRef{{Pool: "talk"}}},
		},
	}
	s := newStation(t, plan, []Source{show}, cat, now)

	// Make the station notice the episode, exactly as a decision would.
	if _, _, _, err := s.engine.Decide(context.Background(), now, ProgramState{}); err != nil {
		t.Fatalf("decide: %v", err)
	}
	owed := s.engine.pendingObligations(context.Background(), now)
	if owed.Len() == 0 {
		t.Fatal("the episode published six hours ago should be owed")
	}

	cond := ConditionContext{
		Window:             time.Hour,
		PoolAvailable:      func(string) bool { return true },
		ObligationsPending: s.engine.pendingObligations(context.Background(), now).Len(),
	}
	if got := ResolveBlock(plan, BuildTimeline(plan, now, time.UTC), ProgramState{}, cond, now); got.Block.ID != "fresh" {
		t.Fatalf("with an episode owed the peek should report %q, got %q", "fresh", got.Block.ID)
	}

	// And the read must not have written anything: a peek is asked on every
	// page load.
	before := owed.Len()
	if after := s.engine.pendingObligations(context.Background(), now).Len(); after != before {
		t.Fatalf("reading the queue changed it: %d -> %d", before, after)
	}
}

// The episode you missed on Tuesday should beat a rerun from 2019.
//
// Once an episode has been surfaced it leaves the obligation queue and becomes
// ordinary back catalogue — which, until recency existed, made it
// indistinguishable from something five years old. But a listener who was out
// for two hours has not heard last week's episode, and that is far likelier to
// be what they want.
func TestTheArchivePrefersWhatYouProbablyMissed(t *testing.T) {
	now := time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC)

	show := podcastSource("pod1", "A Show", "p1")
	episodes := []catalog.PodcastEpisode{
		// Three days old: aired once already, so no longer owed.
		episode("recent", "Last Tuesday's episode", now.AddDate(0, 0, -3), 45),
	}
	for index := 0; index < 15; index++ {
		episodes = append(episodes, episode("ancient"+strconv.Itoa(index),
			"A 2019 rerun "+strconv.Itoa(index), now.AddDate(-5, 0, -index), 45))
	}

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
	}
	s := newStation(t, plan, []Source{show},
		&stubCatalog{episodes: map[string][]catalog.PodcastEpisode{"p1": episodes}}, now)

	item, decision := s.decide()
	if item.ItemRef != "episode:recent" {
		t.Fatalf("played %q over the episode from three days ago\n%s", item.Title, decision.Explain())
	}
}

// Two surfacings for the shows that earn it, one for everything else.
//
// A single airing is right for most of a station's output and wrong for the
// handful of shows it exists to play: go out for two hours and the one episode
// you were waiting for has been and gone. Surfacing EVERYTHING twice is not the
// answer — on this station that would spend most of the day repeating things.
func TestTopTierEpisodesAreSurfacedTwice(t *testing.T) {
	policy := FreshnessPolicy{Surfacings: map[string]int{"S": 2, "A": 2}}

	if got := policy.SurfacingsFor(TierS); got != 2 {
		t.Fatalf("an S-tier show should be surfaced twice, got %v", got)
	}
	if got := policy.SurfacingsFor(TierC); got != 1 {
		t.Fatalf("an untiered show should be surfaced once, got %v", got)
	}

	// One full airing must NOT settle a two-surfacing episode.
	top := Obligation{Tier: TierS, SettleAt: policy.SurfacingsFor(TierS), State: ObligationPending}
	top.Credit = 1.0
	settle(&top, time.Now())
	if top.State != ObligationPending {
		t.Fatalf("one airing settled an episode that is owed two, state %q", top.State)
	}
	top.Credit = 2.0
	settle(&top, time.Now())
	if top.State != ObligationSatisfied {
		t.Fatalf("two airings should settle it, state %q", top.State)
	}

	// And an ordinary show still settles on one.
	ordinary := Obligation{Tier: TierC, SettleAt: policy.SurfacingsFor(TierC), State: ObligationPending}
	ordinary.Credit = 1.0
	settle(&ordinary, time.Now())
	if ordinary.State != ObligationSatisfied {
		t.Fatalf("one airing should settle an ordinary episode, state %q", ordinary.State)
	}

	// A row written before the policy existed keeps the old behaviour.
	legacy := Obligation{Tier: TierS, State: ObligationPending, Credit: 1.0}
	settle(&legacy, time.Now())
	if legacy.State != ObligationSatisfied {
		t.Fatalf("a pre-policy row should settle on one airing, state %q", legacy.State)
	}
}

// Two owed episodes of the SAME show must not go out one after the other.
//
// The obligation queue is ordered by tier then recency, so a show that drops
// twice in a week can easily hold the top two positions. Surfacing what is owed
// is not worth sounding like a broken feed reader — and the relaxation the owed
// path now gets must not become a licence to ignore separation when there is
// something else perfectly good to play.
func TestTwoOwedEpisodesOfOneShowDoNotRunTogether(t *testing.T) {
	now := time.Date(2026, 8, 11, 10, 42, 0, 0, time.UTC)

	stavvys := podcastSource("pod-stav", "Stavvy's World", "pstav")
	stavvys.Config["tier"] = "A"
	other := podcastSource("pod-dough", "Doughboys", "pdough")
	other.Config["tier"] = "B"

	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"pstav": {
			episode("stav-new", "Stavvy's — today", now.Add(-6*time.Hour), 88),
			episode("stav-193", "#193 - Chris Distefano", now.Add(-30*time.Hour), 94),
		},
		"pdough": {episode("dough", "Wainscotting, Hold The Hamm", now.Add(-33*time.Hour), 86)},
	}}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks: []Block{{ID: "fresh", Default: true,
			Pools:   []PoolRef{{Pool: "talk"}},
			Pattern: []PatternStep{{Want: WantObligation}}}},
	}
	s := newStation(t, plan, []Source{stavvys, other}, cat, now)

	// Today's Stavvy's episode just finished.
	s.history.Record(MemoryPlay{
		SourceID: "pod-stav", ItemRef: "episode:stav-new", Category: "talk",
		Artist: "Stavvy's World", StartedAt: now.Add(-95 * time.Minute), EndedAt: now.Add(-7 * time.Minute),
		DurationSeconds: 88 * 60,
	})

	item, decision := s.decide()
	if item.SourceID == "pod-stav" {
		t.Fatalf("played a second Stavvy's World episode seven minutes after the first:\n%s",
			decision.Explain())
	}
	if item.ItemRef != "episode:dough" {
		t.Fatalf("expected the other owed show, got %q", item.Title)
	}
}

// A booked block starts when it says it does.
//
// The station's answer to "there is a gap and nothing fits it" was to bring the
// appointment forward. For a rotation that is fine. For a music hour somebody
// has arranged their day around it is not an hour, it is a surprise — and
// UnderrunPool, the setting that exists to say so, was declared, validated, and
// never read by the engine.
func TestABookedBlockDoesNotStartEarlyWhenTheGapCanBeFilled(t *testing.T) {
	// 12:23, with 37 minutes until a 13:00 music hour, and only long episodes.
	now := time.Date(2026, 8, 11, 12, 23, 0, 0, time.UTC)

	talk := podcastSource("pod1", "A Show", "p1")
	episodes := []catalog.PodcastEpisode{}
	for index := 0; index < 8; index++ {
		// Every one of them too long for the gap.
		episodes = append(episodes, episode("e"+strconv.Itoa(index),
			"A long episode "+strconv.Itoa(index), now.AddDate(0, 0, -30-index), 90))
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 40; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%20), 200))
	}

	plan := Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 1}, {ID: "music", Target: 0}},
		UnderrunPool: "music",
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
		},
		Blocks: []Block{
			{ID: "fresh", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "music-hour", Label: "Music hour",
				Enter: BlockEntry{At: "13:00", Days: "*", Hard: true},
				Exit:  BlockExit{At: "14:00"},
				Next:  "fresh",
				Pools: []PoolRef{{Pool: "music"}}},
		},
	}
	cat := &stubCatalog{
		episodes:  map[string][]catalog.PodcastEpisode{"p1": episodes},
		playlists: map[string][]catalog.MusicTrack{"pl1": songs},
	}
	s := newStation(t, plan, []Source{talk, musicSource("mus1", "House", "pl1")}, cat, now)

	item, decision := s.decide()
	if decision.BlockID == "music-hour" {
		t.Fatalf("the music hour started at 12:23 instead of 13:00:\n%s", decision.Explain())
	}
	if item.Category != "music" {
		t.Fatalf("expected the gap to be filled, got %q (%s)", item.Title, item.Category)
	}
	// And the filler must not itself overrun the hour it is protecting.
	if item.MaxDuration > 37*time.Minute {
		t.Fatalf("the filler could run %s, past the 13:00 start", item.MaxDuration)
	}
}

// The owed list has to say WHICH SHOW, not which source id.
//
// SourceLabel is not a stored column — it is re-derived on read from the source
// row — and a subscription added by picking a podcast has no label of its own.
// So the panel that exists to tell you what you are owed identified episodes by
// raw id, which is how "#193 - Chris Distefano" could sit there unrecognisable
// as a Stavvy's World episode.
func TestTheOwedListNamesTheShow(t *testing.T) {
	now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)

	// No label, exactly as his real sources are.
	unlabelled := podcastSource("pod-stav", "", "pstav")
	unlabelled.Config["tier"] = "A"

	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"pstav": {withShowTitle(
			episode("stav-193", "#193 - Chris Distefano", now.Add(-6*time.Hour), 94),
			"Stavvy's World")},
	}}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks:     []Block{{ID: "fresh", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
	}
	s := newStation(t, plan, []Source{unlabelled}, cat, now)

	queue := s.env().owed
	if len(queue.Pending) == 0 {
		t.Fatal("the episode should be owed")
	}
	if got := queue.Pending[0].SourceLabel; got != "Stavvy's World" {
		t.Fatalf("the owed list called it %q instead of naming the show", got)
	}
}

func withShowTitle(e catalog.PodcastEpisode, title string) catalog.PodcastEpisode {
	e.PodcastTitle = title
	return e
}

// A feed carries things that are not episodes.
//
// "Dr. Drew After Dark Has Ended" is sixty seconds of announcement, and to the
// scheduler it looked like an ideal short item — which made it perfect for the
// gap before a booked show. The slot most likely to be filled with an
// announcement was the one right before something you were waiting for.
func TestAnnouncementsAreNotProgramming(t *testing.T) {
	now := time.Date(2026, 8, 11, 21, 36, 0, 0, time.UTC)

	show := podcastSource("pod1", "Dr. Drew After Dark", "p1")
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p1": {
			episode("ended", "Dr. Drew After Dark Has Ended", now.AddDate(0, 0, -20), 1),
			episode("real", "A real episode", now.AddDate(0, 0, -25), 62),
			// Length unknown — must never be dropped on a guess.
			episode("unmeasured", "An episode nobody has probed", now.AddDate(0, 0, -30), 0),
		},
	}}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		MinItem:    "5m",
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
	}
	s := newStation(t, plan, []Source{show}, cat, now)

	refs := map[string]bool{}
	for _, candidate := range s.engine.Enumerate(context.Background(),
		ProgrammingIntent{Pools: []PoolRef{{Pool: "talk"}}}, s.env()) {
		refs[candidate.Ref] = true
	}
	if refs["episode:ended"] {
		t.Fatal("a one-minute announcement is still being offered as programming")
	}
	if !refs["episode:real"] {
		t.Fatal("the floor dropped a real episode")
	}
	if !refs["episode:unmeasured"] {
		t.Fatal("an episode of unknown length was dropped — unmeasured is not short")
	}
}

// A library that is mostly one artist is a statement of taste.
//
// Separation was fitted to the NUMBER of artists — a hundred and fifteen of
// them can obviously be kept ninety minutes apart — and was blind to the shape
// of the collection. Jacob's playlist is 35% Elvis on purpose, and holding
// Elvis to the same spacing as an artist with one track meant the station spent
// its time refusing to play what it had mostly been given.
func TestADominantArtistIsNotHeldToTheSameSpacing(t *testing.T) {
	now := time.Date(2026, 8, 11, 14, 0, 0, 0, time.UTC)

	candidates := []Candidate{}
	add := func(ref, artist string) {
		candidates = append(candidates, Candidate{
			Ref: ref, Title: ref, SourceID: "mus1", Category: "music",
			Creator: artist, Duration: 4 * time.Minute,
			Traits: Traits{HasCreator: true},
		})
	}
	// The real shape: 132 of 372 are Elvis, the rest spread over many artists.
	for i := 0; i < 132; i++ {
		add("elvis-"+strconv.Itoa(i), "Elvis Presley")
	}
	for i := 0; i < 240; i++ {
		add("other-"+strconv.Itoa(i), "Artist "+strconv.Itoa(i%114))
	}

	env := constraintEnv{
		now:               now,
		separationCreator: 90 * time.Minute,
		lastByCreator:     map[string]time.Time{},
		lastByRef:         map[string]time.Time{},
		lastBySource:      map[string]time.Time{},
		lastByShow:        map[string]time.Time{},
		airings:           map[string]int{},
		lastAirings:       map[string]time.Time{},
		listened:          map[string]bool{},
		categoriesPresent: map[CategoryID]int{"music": 1},
	}
	fitted := fitSeparationToLibrary(env, candidates)

	elvis, ok := fitted.separationByCreator["Elvis Presley"]
	if !ok {
		t.Fatal("the artist who is a third of the library got no allowance at all")
	}
	if elvis >= 30*time.Minute {
		t.Fatalf("Elvis is still held %s apart — a third of the playlist cannot be that rare", elvis)
	}
	t.Logf("Elvis: %s apart (configured %s)", elvis.Round(time.Minute), fitted.separationCreator)

	// A one-track artist keeps the full window — this must only ever relax.
	if _, loosened := fitted.separationByCreator["Artist 7"]; loosened {
		t.Fatal("a rare artist was loosened; the rule should only relax for dominant ones")
	}

	// And Elvis played ten minutes ago is still refused.
	fitted.lastByCreator["Elvis Presley"] = now.Add(-2 * time.Minute)
	for _, rule := range standardConstraints() {
		if rule.Name != "creatorSeparation" {
			continue
		}
		if ok, _ := rule.Check(candidates[0], fitted); ok {
			t.Fatal("two Elvis tracks two minutes apart is not separation at all")
		}
	}
}

// A duration the plan cannot parse must be refused, not quietly defaulted.
//
// Every duration is read through durationOr, which swallows the error and
// returns the DEFAULT — so an unvalidated field does not fail, it means
// something else. longForm.rest was set to "21d", which Go's parser rejects
// because it has no day unit, and became the seven-day default: the giant aired
// three times in three weeks instead of once, and the plan called itself valid
// the whole time.
func TestAPlanRefusesDurationsItCannotRead(t *testing.T) {
	base := func() Plan {
		return Plan{
			Version:    PlanVersion,
			Categories: []CategoryDef{{ID: "talk", Target: 1}},
			Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
			Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
		}
	}

	// Days now parse, because scheduling talks in them.
	got, err := parseDuration("21d")
	if err != nil || got != 21*24*time.Hour {
		t.Fatalf(`parseDuration("21d") = %v, %v`, got, err)
	}

	bad := base()
	bad.LongForm = LongFormPolicy{Rest: "3 weeks"}
	if err := bad.Validate(); err == nil {
		t.Fatal("a plan with an unreadable longForm.rest was accepted, and would silently use the default")
	}

	// "never" is a real answer: only a NEW episode puts a giant on air.
	forever := base()
	forever.LongForm = LongFormPolicy{Threshold: "2h", Rest: "never"}
	if err := forever.Validate(); err != nil {
		t.Fatalf(`longForm.rest "never" should be valid: %v`, err)
	}
	if forever.LongForm.rest() < 50*365*24*time.Hour {
		t.Fatalf("never should be effectively forever, got %s", forever.LongForm.rest())
	}
}

// "never" is not a ban. A NEW episode still goes out.
//
// That is the whole point of the setting: Jacob wants Hardcore History when Dan
// puts one out and not otherwise. If "never" silenced the show completely it
// would be the wrong rule with a friendly name.
func TestANewGiantStillAirsWhenTheShowRestsForever(t *testing.T) {
	now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)

	giant := podcastSource("carlin", "Dan Carlin's Hardcore History", "phh")
	filler := podcastSource("pod1", "Filler", "p1")
	fill := []catalog.PodcastEpisode{}
	for index := 0; index < 12; index++ {
		fill = append(fill, episode("f"+strconv.Itoa(index), "Filler "+strconv.Itoa(index),
			now.AddDate(0, 0, -40-index), 30))
	}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"phh": {
			// Brand new: this is news, not a rerun.
			episode("hh-new", "Mania for Subjugation VII", now.Add(-4*time.Hour), 231),
			episode("hh-old", "An old one", now.AddDate(0, 0, -300), 240),
		},
		"p1": fill,
	}}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks: []Block{{ID: "fresh", Default: true,
			Pools:   []PoolRef{{Pool: "talk"}},
			Pattern: []PatternStep{{Want: WantObligation}}}},
		LongForm: LongFormPolicy{Threshold: "2h", Rest: "never"},
	}
	s := newStation(t, plan, []Source{giant, filler}, cat, now)

	item, decision := s.decide()
	if item.ItemRef != "episode:hh-new" {
		t.Fatalf("a brand-new giant did not air; played %q instead\n%s",
			item.Title, decision.Explain())
	}
	// And once the new one has been surfaced, the show goes quiet again — the
	// back catalogue does not inherit its turn.
	s.play()
	for step := 0; step < 8; step++ {
		next, why := s.step()
		if next.ItemRef == "episode:hh-old" {
			t.Fatalf("the back catalogue giant aired after the new one:\n%s", why.Explain())
		}
	}
}

// A show booked AFTER the plan was written still has to go out.
//
// A stored plan is a snapshot of the schedule at the moment somebody pressed
// save. Book a show afterwards and the rule exists, the UI lists it ENABLED and
// the programme grid draws it — while the scheduler, which reads the PLAN, has
// no block for it. At the appointed hour nothing claims the time and the
// station falls back to ordinary rotation, with no error anywhere. Jacob's
// "Lofi Sleep" at 23:00 was invisible to the scheduler for exactly this reason.
func TestASlotBookedAfterThePlanWasSavedStillAirs(t *testing.T) {
	lofi := Source{ID: "lofi", ChannelID: "c", Kind: SourceLiveStream, Label: "Lofi Sleep",
		Enabled: true, Role: RoleShow, Config: map[string]any{"url": "http://example.test/lofi"}}

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
	}
	// Booked later, so the plan has never heard of it.
	rules := []ScheduleRule{{
		ID: "csched_lofi", ChannelID: "c", SourceID: "lofi", Label: "Lofi Sleep",
		WeekdayMask: 127, StartMinute: 23 * 60, EndMinute: 24 * 60, Enabled: true,
	}}

	adopted, added := plan.AdoptScheduleRules(rules, []Source{lofi})
	if len(added) != 1 {
		t.Fatalf("expected the booked slot to be adopted, got %v", added)
	}
	if err := adopted.Validate(); err != nil {
		t.Fatalf("the adopted plan should be valid: %v", err)
	}

	block, ok := adopted.Block("slot-csched_lofi")
	if !ok {
		t.Fatal("no block for the booked slot")
	}
	if !block.Enter.Hard || block.Enter.At != "23:00" {
		t.Fatalf("the adopted slot is not booked where the rule says: %+v", block.Enter)
	}
	// Midnight renders as 00:00, and the wrap is handled by the timeline.
	if block.Exit.At != "00:00" {
		t.Fatalf("the adopted slot ends at %q, not midnight", block.Exit.At)
	}

	// At 23:00 it must be what is on air, not the rotation.
	now := time.Date(2026, 8, 11, 23, 10, 0, 0, time.UTC)
	timeline := BuildTimeline(adopted, now, time.UTC)
	got := ResolveBlock(adopted, timeline, ProgramState{}, ConditionContext{}, now)
	if got.Block.ID != "slot-csched_lofi" {
		t.Fatalf("at 23:10 the station is in %q, not the booked slot", got.Block.ID)
	}

	// Adopting twice must not duplicate it.
	again, addedAgain := adopted.AdoptScheduleRules(rules, []Source{lofi})
	if len(addedAgain) != 0 || len(again.Blocks) != len(adopted.Blocks) {
		t.Fatalf("adoption is not idempotent: added %v", addedAgain)
	}
}

// Two booked blocks back to back must not leave a hole at the join — and must
// not close it by moving the join.
//
// The last ninety seconds of a music block, where no track fits before the news
// hour, used to be handed to ordinary programming — which was asked to fill
// ninety seconds with a forty-minute episode, could not, and went silent at the
// same minute every single day. The answer to that was to start the news early,
// which traded dead air for a bulletin that opens before the hour.
//
// Neither is necessary. A music hour with a minute left wants one more song,
// faded out on the hour: the hole is filled with the block's own content and
// the news still starts at 18:30.
func TestBackToBackBookedBlocksLeaveNoHole(t *testing.T) {
	// 18:29, one minute before the news, inside a music block that ends 18:30.
	now := time.Date(2026, 8, 11, 18, 29, 0, 0, time.UTC)

	music := musicSource("mus1", "House", "pl1")
	news := Source{ID: "krcc", ChannelID: "c", Kind: SourceLiveStream, Label: "KRCC",
		Enabled: true, Role: RoleShow, Config: map[string]any{"url": "http://example.test/krcc"}}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 40; index++ {
		// Every track longer than the sliver that is left.
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%20), 240))
	}

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}, {ID: "music", Target: 0}},
		Pools: []Pool{
			{ID: "music", Match: &PoolMatch{Category: "music"}},
			{ID: "news", SourceIDs: []string{"krcc"}},
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
		},
		Blocks: []Block{
			{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "music-hour", Label: "Music hour",
				Enter: BlockEntry{At: "17:00", Days: "*", Hard: true},
				Exit:  BlockExit{At: "18:30"}, Next: "general",
				Pools: []PoolRef{{Pool: "music"}}},
			{ID: "news-hour", Label: "KRCC",
				Enter: BlockEntry{At: "18:30", Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "20:00"},
				Pools: []PoolRef{{Pool: "news"}}},
		},
	}
	cat := &stubCatalog{playlists: map[string][]catalog.MusicTrack{"pl1": songs}}
	s := newStation(t, plan, []Source{music, news}, cat, now)
	s.state = ProgramState{BlockID: "music-hour", EnteredAt: now.Add(-89 * time.Minute), ItemCount: 20}
	// Something has already aired in the slot, so the remaining time is a real
	// fit constraint again — the opening item of a booked block is allowed to
	// overrun, and this is not it.
	s.history.Record(MemoryPlay{
		SourceID: "mus1", ItemRef: "track:t0", Category: "music", Artist: "Artist 0",
		StartedAt: now.Add(-4 * time.Minute), EndedAt: now, DurationSeconds: 240,
	})

	item, decision, _, err := s.engine.Decide(context.Background(), now, s.state)
	if err != nil {
		t.Fatalf("dead air at the join between two booked blocks: %v (%s)", err, decision.Error)
	}
	if item.SourceID == "krcc" {
		t.Fatalf("the news started at 18:29 instead of 18:30:\n%s", decision.Explain())
	}
	if item.SourceID != "mus1" {
		t.Fatalf("expected the music hour to hold its own last minute, got %q from %q",
			item.Title, item.SourceID)
	}
	// And it lands ON the join: capped at what is left of the hour, faded
	// rather than cut off mid-chorus.
	if item.MaxDuration != time.Minute {
		t.Fatalf("the last song runs %s; the hour has a minute left", item.MaxDuration)
	}
	if item.FadeOut <= 0 {
		t.Fatalf("a song the clock will take must be faded, got %s", item.FadeOut)
	}
}

// A bounded block should not paint itself into a corner.
//
// Playing greedily inside a booked hour leaves whatever is left over, and that
// is eventually ninety seconds — shorter than anything the station owns. A
// person filling that hour reaches for something that LANDS on the boundary
// instead of leaving a stub nothing can fill.
func TestABoundedBlockPicksSomethingThatLandsOnTheBoundary(t *testing.T) {
	// Ten minutes of a music block left.
	ceiling := 10 * time.Minute
	mk := func(ref string, minutes int) Candidate {
		return Candidate{Ref: ref, Title: ref, SourceID: "mus1", Category: "music",
			Duration: time.Duration(minutes) * time.Minute}
	}
	// Shortest thing available is 2m. An 9m track leaves a 1m stub; a 8m track
	// leaves 2m, which is playable; a 10m track lands exactly.
	candidates := []Candidate{
		mk("nine", 9), mk("eight", 8), mk("ten", 10), mk("two", 2),
	}

	kept := preferNoStub(candidates, ceiling, nil)
	refs := map[string]bool{}
	for _, c := range kept {
		refs[c.Ref] = true
	}
	if refs["nine"] {
		t.Fatal("kept the choice that leaves a one-minute stub nothing can fill")
	}
	for _, want := range []string{"eight", "ten", "two"} {
		if !refs[want] {
			t.Fatalf("dropped %q, which leaves a playable remainder", want)
		}
	}

	// When EVERY choice leaves a stub, the station still plays.
	onlyStubs := []Candidate{mk("a", 9), mk("b", 9)}
	if got := preferNoStub(onlyStubs, ceiling, nil); len(got) != 2 {
		t.Fatalf("with no clean option the station must still play, got %d candidates", len(got))
	}

	// Unbounded blocks are untouched.
	if got := preferNoStub(candidates, 0, nil); len(got) != len(candidates) {
		t.Fatal("an unbounded block should not be filtered")
	}
}

// A six-year-old episode must not be interchangeable with last month's.
//
// From the real station, 2026-08-11 13:12. Every one of the thirty contenders
// was older than the fourteen-day recency horizon, past which the term returned
// a flat zero — so age had no bearing on the choice at all, the whole band
// scored alike, and the weighted pick handed the afternoon to a Hardcore
// History episode Dan Carlin put out years ago. Jacob's words: "hardcore
// history released that episode several years ago."
//
// The station is supposed to play its back catalogue. It is not supposed to be
// indifferent about how far back.
func TestAncientBackCatalogueIsNotAContender(t *testing.T) {
	now := time.Date(2026, 8, 11, 13, 12, 0, 0, time.UTC)
	sources := []Source{
		podcastSource("pod1", "Show One", "p1"),
		podcastSource("pod2", "Show Two", "p2"),
		musicSource("mus1", "House Playlist", "pl1"),
	}
	cat := &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{
			// Same show, same length, neither ever aired: age is the only thing
			// separating them.
			"p1": {
				episode("recent", "Last month", now.AddDate(0, 0, -20), 40),
				episode("ancient", "Six years ago", now.AddDate(-6, 0, 0), 40),
			},
			"p2": {episode("other", "Another show", now.AddDate(0, 0, -25), 40)},
		},
		playlists: map[string][]catalog.MusicTrack{
			"pl1": {track("t1", "Song one", "Artist A", 210)},
		},
	}
	s := newStation(t, twoCategoryPlan(0.75), sources, cat, now)
	_, decision := s.decide()

	var recent, ancient *CandidateSummary
	for index := range decision.Candidates {
		switch decision.Candidates[index].Ref {
		case "episode:recent":
			recent = &decision.Candidates[index]
		case "episode:ancient":
			ancient = &decision.Candidates[index]
		}
	}
	if recent == nil || ancient == nil {
		t.Fatalf("both episodes should have been scored; got %d candidates\n%s",
			len(decision.Candidates), decision.Explain())
	}
	if ancient.Score >= recent.Score {
		t.Fatalf("a six-year-old episode scored %.3f against last month's %.3f —"+
			" the scorer has no opinion about age\n%s",
			ancient.Score, recent.Score, decision.Explain())
	}
	if ancient.Contender {
		t.Fatalf("a six-year-old episode was in the running against last month's"+
			" (%.3f vs %.3f); the random draw can hand it the afternoon\n%s",
			ancient.Score, recent.Score, decision.Explain())
	}
}

// No room for the new episode means no room for the show.
//
// From the real station, 2026-08-11 15:04. Joey Diaz published seventy-one
// minutes at 09:10 that morning. Fifty-five minutes remained before All Things
// Considered, so the new episode was refused — "1h11m0s long, but only 55m0s
// until the next booked slot", which is correct and never bends. The station
// then played "#244 | UNCLE JOEY'S JOINT with JOEY DIAZ", thirty-three minutes,
// published June 2023, out of the SAME feed, because that one fit.
//
// Jacob: "Why is it just playing an old episode of joey diaz? There's a brand
// fucking new one and it's playing this one."
func TestNoRoomForTheNewEpisodeMeansNoRoomForTheShow(t *testing.T) {
	now := time.Date(2026, 8, 11, 15, 4, 0, 0, time.UTC)
	sources := []Source{
		podcastSource("joey", "The Church of What's Happening Now", "pjoey"),
		podcastSource("other", "Another Show", "pother"),
		musicSource("mus1", "House Playlist", "pl1"),
		{ID: "atc", ChannelID: "ch1", Kind: SourceLiveStream, Label: "ATC", Enabled: true,
			Role: RoleShow, Config: map[string]any{"url": "http://example.test/atc"}},
	}
	cat := &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{
			"pjoey": {
				// This morning's, and too long for the gap.
				episode("greyhound", "The Greyhound to Hell", now.Add(-6*time.Hour), 71),
				// June 2023, and short enough to fit.
				episode("ep244", "#244 | UNCLE JOEY'S JOINT", now.AddDate(-3, -2, 0), 33),
			},
			// Deliberately OLDER than the Joey Diaz rerun, so recency cannot be
			// what saves this. Without the show rule the 2023 Joey Diaz is the
			// better-scoring candidate and wins outright.
			"pother": {episode("other1", "Someone else entirely", now.AddDate(-3, -8, 0), 30)},
		},
		playlists: map[string][]catalog.MusicTrack{
			"pl1": {track("t1", "Song one", "Artist A", 210)},
		},
	}

	// His station's shape: talk is the format, music is a separator, and the
	// general block reaches for spoken word only.
	plan := Plan{
		Version: PlanVersion,
		Seed:    3,
		// No random draw: the single best-scoring candidate always wins, so the
		// test measures the rule and not the dice.
		Selection:  SelectionPolicy{Epsilon: -1},
		Categories: []CategoryDef{{ID: "talk", Target: 1}, {ID: "music", Target: 0}},
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
			{ID: "booked", SourceIDs: []string{"atc"}},
		},
		Blocks: []Block{
			{ID: "general", Label: "General rotation", Default: true,
				Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "atc", Label: "All Things Considered",
				Enter: BlockEntry{At: "16:00", Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "17:00"},
				Pools: []PoolRef{{Pool: "booked"}}, Next: "general"},
		},
	}

	s := newStation(t, plan, sources, cat, now)
	if err := s.engine.Obligations.Notice(context.Background(), []Obligation{{
		ChannelID: "ch1", ItemRef: "episode:greyhound", Title: "The Greyhound to Hell",
		SourceID: "joey", Tier: "C", PublishedAt: now.Add(-6 * time.Hour),
		NoticedAt: now.Add(-6 * time.Hour), ExpiresAt: now.Add(66 * time.Hour),
		SettleAt: 1,
	}}, now); err != nil {
		t.Fatalf("notice: %v", err)
	}

	item, decision := s.decide()
	if item.ItemRef == "episode:ep244" {
		t.Fatalf("refused this morning's 71-minute episode for not fitting the 55-minute gap,"+
			" then played a 2023 episode of the SAME show\n%s", decision.Explain())
	}
	if item.ItemRef == "" {
		t.Fatalf("played nothing at all — it should have reached for a different show\n%s",
			decision.Explain())
	}
	t.Logf("played %q from %s instead", item.Title, item.SourceID)
}
