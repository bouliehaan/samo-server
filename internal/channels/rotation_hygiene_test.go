package channels

import (
	"context"
	"strconv"
	"strings"
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
		lastByCreator:     map[string]lastAiring{},
		lastByRef:         map[string]time.Time{},
		lastBySource:      map[string]lastAiring{},
		lastByShow:        map[string]lastAiring{},
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
	fitted.lastByCreator["Elvis Presley"] = fullyHeard(now.Add(-2 * time.Minute))
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

	adopted, added, _ := plan.ReconcileScheduleRules(rules, []Source{lofi})
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
	again, addedAgain, _ := adopted.ReconcileScheduleRules(rules, []Source{lofi})
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

// Cancelling a booking must take it off the air.
//
// The mirror image of the bug above, and the one that actually bit: a booked
// slot is copied into the stored plan the first time anybody saves it, and
// nothing ever took the copy back out. Delete the rule and it vanishes from the
// SCHEDULE list, from the programme grid and from every screen that reads the
// schedule — while the block it was copied into keeps claiming the same hour
// every day, for ever, with no rule anywhere to explain it. NPR kept going past
// seven every weekday evening for exactly this reason.
func TestCancellingABookingTakesItOffTheAir(t *testing.T) {
	npr := Source{ID: "csrc_npr", ChannelID: "c", Kind: SourceLiveStream, Label: "NPR",
		Enabled: true, Role: RoleShow, Config: map[string]any{"url": "http://example.test/npr"}}

	// The plan as it is stored after somebody has saved it once: the booked
	// slot has been baked in.
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "slot-csrc_npr", Label: "NPR", SourceIDs: []string{"csrc_npr"}},
		},
		Blocks: []Block{
			{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{
				ID:    "slot-csched_npr_evening",
				Label: "NPR evening",
				Enter: BlockEntry{At: "19:00", Days: "mon-fri", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "21:00"},
				Pools: []PoolRef{{Pool: "slot-csrc_npr", Weight: 1}},
			},
		},
	}

	// The rule behind it has been cancelled, so the schedule is empty.
	reconciled, added, dropped := plan.ReconcileScheduleRules(nil, []Source{npr})
	if len(added) != 0 {
		t.Fatalf("nothing is booked, so nothing should have been added: %v", added)
	}
	if len(dropped) != 1 {
		t.Fatalf("the cancelled slot should have been dropped, got %v", dropped)
	}
	if _, ok := reconciled.Block("slot-csched_npr_evening"); ok {
		t.Fatal("the block for a cancelled booking is still in the plan")
	}
	if err := reconciled.Validate(); err != nil {
		t.Fatalf("the reconciled plan should be valid: %v", err)
	}

	// And the station must be in ordinary rotation at the hour it used to hold.
	now := time.Date(2026, 8, 20, 19, 30, 0, 0, time.UTC) // a Thursday
	timeline := BuildTimeline(reconciled, now, time.UTC)
	got := ResolveBlock(reconciled, timeline, ProgramState{}, ConditionContext{}, now)
	if got.Block.ID != "general" {
		t.Fatalf("at 19:30 on a weekday the station is in %q, not ordinary rotation", got.Block.ID)
	}
}

// Disabling a rule is the same answer as deleting it: the slot stops.
func TestADisabledBookingDoesNotHoldItsSlot(t *testing.T) {
	npr := Source{ID: "csrc_npr", ChannelID: "c", Kind: SourceLiveStream, Label: "NPR",
		Enabled: true, Role: RoleShow, Config: map[string]any{"url": "http://example.test/npr"}}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks: []Block{
			{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{
				ID:    "slot-csched_npr",
				Enter: BlockEntry{At: "19:00", Hard: true},
				Exit:  BlockExit{At: "21:00"},
				Pools: []PoolRef{{Pool: "talk"}},
			},
		},
	}
	rules := []ScheduleRule{{ID: "csched_npr", ChannelID: "c", SourceID: "csrc_npr",
		WeekdayMask: 62, StartMinute: 19 * 60, EndMinute: 21 * 60, Enabled: false}}

	reconciled, _, dropped := plan.ReconcileScheduleRules(rules, []Source{npr})
	if len(dropped) != 1 {
		t.Fatalf("a disabled rule should not hold its block, dropped %v", dropped)
	}
	if _, ok := reconciled.Block("slot-csched_npr"); ok {
		t.Fatal("a disabled booking still has a block")
	}
}

// A booking that has moved takes its block with it.
//
// The plan's copy is a copy. Leaving it on the old clock time means the SCHEDULE
// list and the station disagree about when a show is on, which is the same
// class of bug as the two above with a smaller blast radius.
func TestARebookedSlotMovesItsBlock(t *testing.T) {
	npr := Source{ID: "csrc_npr", ChannelID: "c", Kind: SourceLiveStream, Label: "NPR",
		Enabled: true, Role: RoleShow, Config: map[string]any{"url": "http://example.test/npr"}}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "slot-csrc_npr", SourceIDs: []string{"csrc_npr"}},
		},
		Blocks: []Block{
			{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{
				ID:    "slot-csched_npr",
				Label: "NPR evening",
				Enter: BlockEntry{At: "19:00", Days: "mon-fri", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "21:00"},
				Pools: []PoolRef{{Pool: "slot-csrc_npr", Weight: 1}},
			},
		},
	}
	// Same booking, now finishing at seven instead of running to nine.
	rules := []ScheduleRule{{ID: "csched_npr", ChannelID: "c", SourceID: "csrc_npr",
		Label: "NPR evening", WeekdayMask: 62, StartMinute: 17 * 60, EndMinute: 19 * 60, Enabled: true}}

	reconciled, _, _ := plan.ReconcileScheduleRules(rules, []Source{npr})
	block, ok := reconciled.Block("slot-csched_npr")
	if !ok {
		t.Fatal("the booking lost its block")
	}
	if block.Enter.At != "17:00" || block.Exit.At != "19:00" {
		t.Fatalf("the block did not follow its rule: enters %q, exits %q", block.Enter.At, block.Exit.At)
	}
}

// A block somebody named `slot-…` by hand belongs to them.
//
// The reconcile owns the ids it writes — `slot-` plus a rule id — and nothing
// else. Pruning on the prefix alone would quietly delete a hand-written block
// on every plan load.
func TestReconcileLeavesHandWrittenSlotNamesAlone(t *testing.T) {
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", Match: &PoolMatch{Category: "talk"}}},
		Blocks: []Block{
			{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "slot-overnight", Label: "Overnight", Enter: BlockEntry{At: "01:00"}, Pools: []PoolRef{{Pool: "talk"}}},
		},
	}
	reconciled, _, dropped := plan.ReconcileScheduleRules(nil, nil)
	if len(dropped) != 0 {
		t.Fatalf("a hand-written block was dropped: %v", dropped)
	}
	if _, ok := reconciled.Block("slot-overnight"); !ok {
		t.Fatal("the hand-written block is gone")
	}
}

// The run-up to a booked slot is not the place for the shortest oddity a
// station owns.
//
// Jacob's words: "when we get close to a scheduled slot, oftentimes we end up
// playing like one or 2 episodes I have of things that are like only a few
// minutes long. Why we no just play music?"
//
// Because nothing had ever asked the question. Ten minutes before the news,
// fitsBeforeAnchor rules out every real episode on the station and the two
// four-minute curios in the library are the only spoken things left standing —
// so on a talk-format channel, where the talk category is permanently in
// deficit, they beat music outright. That pick is the highest-scoring candidate
// that fits, the record says so, and it is still wrong: the clock chose it, not
// the station.
func TestTheGapBeforeABookedSlotGoesToMusic(t *testing.T) {
	now := time.Date(2026, 8, 27, 15, 50, 0, 0, time.UTC)
	sources := []Source{
		podcastSource("longpod", "A Long Podcast", "plong"),
		podcastSource("shortpod", "Minute Briefs", "pshort"),
		musicSource("mus1", "House Playlist", "pl1"),
		{ID: "atc", ChannelID: "ch1", Kind: SourceLiveStream, Label: "ATC", Enabled: true,
			Role: RoleShow, Config: map[string]any{"url": "http://example.test/atc"}},
	}

	// The shelf: a dozen real episodes, none of which fit ten minutes, and the
	// two curios that do.
	long := make([]catalog.PodcastEpisode, 0, 12)
	for i := 0; i < 12; i++ {
		long = append(long, episode("long"+strconv.Itoa(i), "Long episode "+strconv.Itoa(i),
			now.AddDate(0, 0, -3*i-1), 45+i))
	}
	tracks := make([]catalog.MusicTrack, 0, 24)
	for i := 0; i < 24; i++ {
		tracks = append(tracks, track("t"+strconv.Itoa(i), "Song "+strconv.Itoa(i),
			"Artist "+strconv.Itoa(i), 150+(i*37)%180))
	}
	// The curios come with a deep back catalogue, because that is what a daily
	// short-form show actually looks like: sixty rows against the long show's
	// twelve. A fixture with two of them passes whether the rule works or not.
	briefs := make([]catalog.PodcastEpisode, 0, 60)
	for i := 0; i < 60; i++ {
		briefs = append(briefs, episode("brief"+strconv.Itoa(i), "Brief "+strconv.Itoa(i),
			now.AddDate(0, 0, -i-1), 3+i%3))
	}
	cat := &stubCatalog{
		episodes:  map[string][]catalog.PodcastEpisode{"plong": long, "pshort": briefs},
		playlists: map[string][]catalog.MusicTrack{"pl1": tracks},
	}

	// His station's shape: talk is the format and music is the minority, so
	// categoryDeficit points at talk at every decision.
	plan := Plan{
		Version:      PlanVersion,
		Seed:         3,
		Selection:    SelectionPolicy{Epsilon: -1},
		Categories:   []CategoryDef{{ID: "talk", Target: 0.7}, {ID: "music", Target: 0.3}},
		UnderrunPool: "music",
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
			{ID: "booked", SourceIDs: []string{"atc"}},
		},
		Blocks: []Block{
			{ID: "general", Label: "General rotation", Default: true,
				Pools: []PoolRef{{Pool: "talk"}, {Pool: "music"}}},
			{ID: "atc", Label: "All Things Considered",
				Enter: BlockEntry{At: "16:00", Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "17:00"},
				Pools: []PoolRef{{Pool: "booked"}}, Next: "general"},
		},
	}

	s := newStation(t, plan, sources, cat, now)
	slot := time.Date(2026, 8, 27, 16, 0, 0, 0, time.UTC)
	for s.now.Before(slot) {
		at := s.now
		item, decision := s.step()
		if item.SourceID == "shortpod" {
			t.Fatalf("at %s, with %s to go before the news, the station reached for a"+
				" %ds curio instead of music\n%s",
				at.Format("15:04:05"), round(slot.Sub(at)), item.DurationSeconds, decision.Explain())
		}
		if item.SourceID == "atc" {
			t.Fatalf("the booked slot opened at %s instead of 16:00 — the gap has to be"+
				" FILLED, not handed back\n%s", at.Format("15:04:05"), decision.Explain())
		}
	}

	// And the appointment still starts on the second: filling the gap must not
	// have cost the boundary it exists to protect.
	if !s.now.Equal(slot) {
		t.Fatalf("the run-up ran to %s, not 16:00 exactly", s.now.Format("15:04:05"))
	}

	// With the slot behind it the station goes back to real programming: the
	// curios were deferred, not banned.
	s.now = slot.Add(time.Hour)
	s.state = ProgramState{}
	item, decision := s.decide()
	if item.Category != "talk" {
		t.Fatalf("after the slot the station should be back on talk, played %q from %s\n%s",
			item.Title, item.SourceID, decision.Explain())
	}
}

// A station whose talk IS four minutes long keeps its four minutes.
//
// The rule above measures a stub against what its own category normally runs,
// and that is the whole of its safety: a news brief, a weather bed, a channel
// of short readings are all representative at four minutes, and a rule written
// in absolute seconds would have silently taken every one of them off the air
// in front of every appointment on the schedule.
func TestAShortFormatIsNotAStub(t *testing.T) {
	now := time.Date(2026, 8, 27, 15, 50, 0, 0, time.UTC)
	window := 10 * time.Minute

	brief := func(ref string, minutes int) Candidate {
		return Candidate{Ref: ref, Title: ref, SourceID: "briefs", Category: "talk",
			Duration: time.Duration(minutes) * time.Minute}
	}
	song := func(ref string) Candidate {
		return Candidate{Ref: ref, Title: ref, SourceID: "mus1", Category: "music",
			Duration: 3 * time.Minute}
	}
	env := constraintEnv{now: now, window: window}

	// A category that is short all the way down. Nothing of it is longer than
	// the gap, so the gap has taken nothing away and the rule has no business
	// firing.
	shortFormat := []Candidate{brief("b1", 4), brief("b2", 3), brief("b3", 5), song("s1")}
	if blocked := categoriesOutOfRoom(shortFormat, shortFormat, env); len(blocked) > 0 {
		t.Fatalf("stood a four-minute format down from a ten-minute gap: %v", blocked)
	}

	// The same four-minute item in a category of forty-five-minute episodes IS
	// a stub, and only because the gap is what left it standing alone.
	longFormat := []Candidate{
		brief("curio", 4), brief("e1", 45), brief("e2", 50), brief("e3", 40), song("s1"),
	}
	survivors := []Candidate{brief("curio", 4), song("s1")}
	blocked := categoriesOutOfRoom(longFormat, survivors, env)
	if !blocked["talk"] {
		t.Fatal("left a four-minute curio to represent a forty-five-minute format in a ten-minute gap")
	}
	if blocked["music"] {
		t.Fatal("stood music down: a three-minute song is exactly what a ten-minute gap is for")
	}

	// An episode that is genuinely worth the gap keeps the category in play,
	// even though the giants of it were ruled out.
	withRealOption := append([]Candidate{brief("mid", 20)}, longFormat...)
	stillFits := []Candidate{brief("curio", 4), brief("mid", 20), song("s1")}
	if got := categoriesOutOfRoom(withRealOption, stillFits, constraintEnv{
		now: now, window: 25 * time.Minute,
	}); len(got) > 0 {
		t.Fatalf("stood talk down with a twenty-minute episode fitting the gap: %v", got)
	}

	// No appointment, no rule: an open-ended rotation is not a gap.
	if got := categoriesOutOfRoom(longFormat, survivors, constraintEnv{now: now}); len(got) > 0 {
		t.Fatalf("fired with no boundary ahead: %v", got)
	}

	// And never during a boundary fill, where being cut off is the job and
	// every length on the station is a candidate.
	if got := categoriesOutOfRoom(longFormat, survivors, constraintEnv{
		now: now, window: window, cutAtBoundary: true,
	}); len(got) > 0 {
		t.Fatalf("fired inside a boundary fill: %v", got)
	}

	// Something with no natural end — a relayed stream — is not a stub. It is
	// capped at the gap instead, so its category still has a real answer.
	relay := []Candidate{
		{Ref: "krcc", SourceID: "krcc", Category: "talk", Traits: Traits{Continuous: true}},
		song("s1"),
	}
	if got := categoriesOutOfRoom(longFormat, relay, env); len(got) > 0 {
		t.Fatalf("called a continuous source a stub: %v", got)
	}

	// But an episode nobody has MEASURED is not a relay, and reading the two as
	// one thing is what made this rule miss most of what it was written for. A
	// feed that omits its duration arrives here at zero, and one of those
	// anywhere in the category used to clear the whole category — every real
	// episode back into contention, the clock keeping its pick, and the record
	// showing the rule quietly not firing.
	unprobed := []Candidate{
		brief("curio", 4),
		{Ref: "nolength", SourceID: "briefs", Category: "talk"},
		song("s1"),
	}
	if got := categoriesOutOfRoom(longFormat, unprobed, env); !got["talk"] {
		t.Fatal("one unmeasured episode waved the whole category back into a gap it does not fit")
	}
}

// A short show with a deep back catalogue must not redefine what its category
// is.
//
// The rule above asks what a category normally runs. Asked of the enumerated
// EPISODES, the answer is decided by which shows publish most often — and short
// shows publish often. Three long-form shows with twenty episodes each are
// outvoted two to one by two five-minute dailies with sixty apiece: the median
// lands at five minutes, the floor under two, and every curio the rule exists
// to catch reads as perfectly typical programming.
//
// Which is not a corner case. It is the ordinary shape of a podcast library,
// and it is why the first cut of this rule fired in tests and did nothing on
// the actual station.
func TestADeepBackCatalogueDoesNotRedefineItsCategory(t *testing.T) {
	window := 10 * time.Minute
	env := constraintEnv{now: time.Date(2026, 8, 28, 15, 50, 0, 0, time.UTC), window: window}

	shelf := []Candidate{}
	// Three long-form shows, twenty episodes each.
	for show := 0; show < 3; show++ {
		for i := 0; i < 20; i++ {
			shelf = append(shelf, Candidate{
				Ref:      "l" + strconv.Itoa(show) + "_" + strconv.Itoa(i),
				SourceID: "long" + strconv.Itoa(show), Show: "Long Show " + strconv.Itoa(show),
				Category: "talk", Duration: time.Duration(42+i%8) * time.Minute,
			})
		}
	}
	// Two five-minute dailies, sixty episodes each: four times the row count.
	for show := 0; show < 2; show++ {
		for i := 0; i < 60; i++ {
			shelf = append(shelf, Candidate{
				Ref:      "s" + strconv.Itoa(show) + "_" + strconv.Itoa(i),
				SourceID: "short" + strconv.Itoa(show), Show: "Daily " + strconv.Itoa(show),
				Category: "talk", Duration: time.Duration(3+i%3) * time.Minute,
			})
		}
	}
	shelf = append(shelf, Candidate{Ref: "song", SourceID: "mus1", Category: "music",
		Duration: 3 * time.Minute})

	if floor := categoryStubFloors(shelf)["talk"]; floor < 10*time.Minute {
		t.Fatalf("a category of forty-five-minute shows came out with a %v floor — counted"+
			" episodes rather than shows, so nothing in it can ever be a stub", floor)
	}

	survivors := []Candidate{
		{Ref: "s0_1", SourceID: "short0", Show: "Daily 0", Category: "talk", Duration: 4 * time.Minute},
		{Ref: "song", SourceID: "mus1", Category: "music", Duration: 3 * time.Minute},
	}
	if !categoriesOutOfRoom(shelf, survivors, env)["talk"] {
		t.Fatal("left a four-minute daily to represent a category of forty-five-minute shows")
	}

	// And a station that is ALL dailies keeps them: counting shows rather than
	// episodes must not turn into a bias against short formats.
	allShort := shelf[60:]
	shortSurvivors := []Candidate{allShort[0], survivors[1]}
	if got := categoriesOutOfRoom(allShort, shortSurvivors, env); len(got) > 0 {
		t.Fatalf("stood a station of five-minute dailies down from a ten-minute gap: %v", got)
	}
}

// A station with nothing else to offer keeps talking.
//
// The rule above stands a category down in favour of something better suited to
// the gap. Where there is no something-else — a channel that is spoken word and
// nothing but — standing down would mean silence in front of every appointment
// on the schedule, which is worse than any curio. Preferring music over a stub
// is a preference; having something to play is not.
func TestTheGapRuleNeverLeavesTheStationWithNothing(t *testing.T) {
	now := time.Date(2026, 8, 27, 15, 50, 0, 0, time.UTC)
	sources := []Source{
		podcastSource("longpod", "A Long Podcast", "plong"),
		podcastSource("shortpod", "Minute Briefs", "pshort"),
		{ID: "atc", ChannelID: "ch1", Kind: SourceLiveStream, Label: "ATC", Enabled: true,
			Role: RoleShow, Config: map[string]any{"url": "http://example.test/atc"}},
	}
	long := make([]catalog.PodcastEpisode, 0, 8)
	for i := 0; i < 8; i++ {
		long = append(long, episode("long"+strconv.Itoa(i), "Long episode "+strconv.Itoa(i),
			now.AddDate(0, 0, -3*i-1), 45+i))
	}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"plong":  long,
		"pshort": {episode("brief1", "A three minute brief", now.AddDate(0, 0, -9), 3)},
	}}
	plan := Plan{
		Version:    PlanVersion,
		Seed:       3,
		Selection:  SelectionPolicy{Epsilon: -1},
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "booked", SourceIDs: []string{"atc"}},
		},
		Blocks: []Block{
			{ID: "general", Label: "General rotation", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "atc", Label: "All Things Considered",
				Enter: BlockEntry{At: "16:00", Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "17:00"},
				Pools: []PoolRef{{Pool: "booked"}}, Next: "general"},
		},
	}

	s := newStation(t, plan, sources, cat, now)
	item, decision := s.decide()
	if item.ItemRef != "episode:brief1" {
		t.Fatalf("a talk-only station in a ten-minute gap must still play its only option,"+
			" played %q\n%s", item.Title, decision.Explain())
	}
	// And it does not claim to have done something it did not do.
	if strings.Contains(decision.Note, "odds and ends") {
		t.Fatalf("recorded that the gap went elsewhere, then played the stub anyway: %q", decision.Note)
	}
}

// The last sliver in front of a slot is still not the place for a curio.
//
// The gap rule stands a category down by taking it out of contention, and
// dropCategories will not empty a candidate set — a station with nowhere else
// to go keeps playing. So in the last stretch, where no song fits either, the
// rule found the right answer and could not apply it: the curio was all that
// was left, and it went out. On a real station that is one short episode in
// front of a booked show most days, which is exactly the complaint.
//
// "The only things that fit are odds and ends" is a block out of ROOM, not out
// of options, and the plan already nominates a pool for that moment. Ask it,
// and fall back to the curio only if it comes back empty.
func TestTheLastSliverBeforeASlotIsFilledNotScraped(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 57, 0, 0, time.UTC)
	sources := []Source{
		podcastSource("longpod", "A Long Podcast", "plong"),
		podcastSource("shortpod", "A Daily Brief", "pshort"),
		musicSource("mus1", "House Playlist", "pl1"),
		{ID: "atc", ChannelID: "ch1", Kind: SourceLiveStream, Label: "ATC", Enabled: true,
			Role: RoleShow, Config: map[string]any{"url": "http://example.test/atc"}},
	}
	long := make([]catalog.PodcastEpisode, 0, 10)
	for i := 0; i < 10; i++ {
		long = append(long, episode("long"+strconv.Itoa(i), "Long ep "+strconv.Itoa(i),
			now.AddDate(0, 0, -3*i-1), 45+i))
	}
	briefs := make([]catalog.PodcastEpisode, 0, 40)
	for i := 0; i < 40; i++ {
		briefs = append(briefs, episode("brief"+strconv.Itoa(i), "Brief "+strconv.Itoa(i),
			now.AddDate(0, 0, -i-1), 3))
	}
	// Three minutes to the slot, and every song on the station is longer than
	// that. Only the three-minute briefs fit — so standing talk down would
	// leave nothing, and the old answer was to play one.
	tracks := make([]catalog.MusicTrack, 0, 12)
	for i := 0; i < 12; i++ {
		tracks = append(tracks, track("t"+strconv.Itoa(i), "Song "+strconv.Itoa(i),
			"Artist "+strconv.Itoa(i), 240+i*5))
	}
	cat := &stubCatalog{
		episodes:  map[string][]catalog.PodcastEpisode{"plong": long, "pshort": briefs},
		playlists: map[string][]catalog.MusicTrack{"pl1": tracks},
	}
	plan := Plan{
		Version:      PlanVersion,
		Seed:         3,
		Selection:    SelectionPolicy{Epsilon: -1},
		Categories:   []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		UnderrunPool: "music",
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
			{ID: "booked", SourceIDs: []string{"atc"}},
		},
		Blocks: []Block{
			{ID: "general", Label: "General rotation", Default: true,
				Pools: []PoolRef{{Pool: "talk"}, {Pool: "music"}}},
			{ID: "atc", Label: "All Things Considered",
				Enter: BlockEntry{At: "16:00", Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "17:00"},
				Pools: []PoolRef{{Pool: "booked"}}, Next: "general"},
		},
	}

	s := newStation(t, plan, sources, cat, now)
	item, decision := s.decide()
	if item.SourceID == "shortpod" {
		t.Fatalf("with three minutes to the news and no song short enough, the station played"+
			" a curio rather than holding the boundary with music\n%s", decision.Explain())
	}
	if item.SourceID != "mus1" {
		t.Fatalf("expected the nominated gap pool, played %q from %s\n%s",
			item.Title, item.SourceID, decision.Explain())
	}
	// Held to the boundary and faded, not run over it.
	if item.MaxDuration <= 0 || item.MaxDuration > 3*time.Minute {
		t.Fatalf("the fill was not capped at the gap: MaxDuration=%v", item.MaxDuration)
	}
}
