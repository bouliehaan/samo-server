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
