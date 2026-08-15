package channels

import (
	"context"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// What the station owes, and the order it owes it in.

func obligation(ref string, tier Tier, published time.Time, window time.Duration) Obligation {
	return Obligation{
		ItemRef:     ref,
		Tier:        tier,
		PublishedAt: published,
		ExpiresAt:   published.Add(window),
		State:       ObligationPending,
	}
}

// The example from the brief, as a test: priority generally matters more than
// small differences in publication time.
func TestTierBeatsRecency(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	window := 72 * time.Hour
	queue := NewObligationQueue([]Obligation{
		obligation("episode:gecko", TierB, now.Add(-10*time.Minute), window),
		obligation("episode:mssp", TierS, now.Add(-6*time.Hour), window),
	}, now, FreshnessPolicy{})

	if len(queue.Pending) != 2 {
		t.Fatalf("expected two pending, got %d", len(queue.Pending))
	}
	if queue.Pending[0].ItemRef != "episode:mssp" {
		t.Fatalf("an S-tier show from six hours ago should outrank a B-tier from ten minutes ago, got %q",
			queue.Pending[0].ItemRef)
	}
}

// Within a tier, newest first — that is what "here is today's episode" means.
func TestRecencyOrdersWithinATier(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	window := 72 * time.Hour
	queue := NewObligationQueue([]Obligation{
		obligation("episode:older", TierB, now.Add(-30*time.Hour), window),
		obligation("episode:newer", TierB, now.Add(-2*time.Hour), window),
	}, now, FreshnessPolicy{})

	if queue.Pending[0].ItemRef != "episode:newer" {
		t.Fatalf("equal tiers order newest first, got %q", queue.Pending[0].ItemRef)
	}
}

// Something about to stop being news climbs, because it is the last chance —
// but not past a tier that is two steps higher.
func TestRunningOutOfTimeLiftsAnObligation(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	window := 72 * time.Hour
	nearlyGone := obligation("episode:expiring", TierC, now.Add(-71*time.Hour), window)
	comfortable := obligation("episode:roomy", TierC, now.Add(-40*time.Hour), window)
	queue := NewObligationQueue([]Obligation{comfortable, nearlyGone}, now, FreshnessPolicy{})
	if queue.Pending[0].ItemRef != "episode:expiring" {
		t.Fatalf("at the same tier, the one about to expire should go first, got %q", queue.Pending[0].ItemRef)
	}

	// Two tiers up still wins, though: expiry is a nudge, not an override.
	withHigher := NewObligationQueue([]Obligation{
		nearlyGone,
		obligation("episode:important", TierA, now.Add(-40*time.Hour), window),
	}, now, FreshnessPolicy{})
	if withHigher.Pending[0].ItemRef != "episode:important" {
		t.Fatalf("an A-tier should outrank an expiring C-tier, got %q", withHigher.Pending[0].ItemRef)
	}
}

// Setting the spread to zero turns the queue back into pure recency, which is
// what the station did before tiers existed. The knob has to actually work, or
// it is decoration.
func TestTierSpreadCanBeTurnedOff(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	window := 72 * time.Hour
	policy := FreshnessPolicy{TierSpread: 0.0001}
	queue := NewObligationQueue([]Obligation{
		obligation("episode:mssp", TierS, now.Add(-6*time.Hour), window),
		obligation("episode:gecko", TierF, now.Add(-10*time.Minute), window),
	}, now, policy)
	if queue.Pending[0].ItemRef != "episode:gecko" {
		t.Fatalf("with the spread all but off, recency should win, got %q", queue.Pending[0].ItemRef)
	}
}

// Credit accumulates and settles. This is the whole exposure model.
func TestCreditAccumulatesAndSettles(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	store := NewMemoryObligations()
	if err := store.Notice(ctx, []Obligation{
		obligation("episode:one", TierB, now.Add(-time.Hour), 72*time.Hour),
	}, now); err != nil {
		t.Fatalf("notice: %v", err)
	}

	// A half-exposure block, played in full.
	if err := store.Credit(ctx, "episode:one", 0.5, now); err != nil {
		t.Fatalf("credit: %v", err)
	}
	pending, _ := store.List(ctx, now)
	if len(pending) != 1 || !pending[0].Pending() {
		t.Fatalf("half a credit should leave it owed, got %+v", pending)
	}

	// And again: now it has had its chance.
	if err := store.Credit(ctx, "episode:one", 0.5, now); err != nil {
		t.Fatalf("credit: %v", err)
	}
	settled, _ := store.List(ctx, now)
	if len(settled) != 1 || settled[0].Pending() {
		t.Fatalf("a full credit should settle it, got %+v", settled)
	}
}

// Noticing the same episode again must not reset what it has already earned.
// A feed is re-read constantly; if noticing were an upsert, nothing would ever
// reach a full credit.
func TestNoticingTwiceDoesNotResetCredit(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	store := NewMemoryObligations()
	first := obligation("episode:one", TierB, now.Add(-time.Hour), 72*time.Hour)
	_ = store.Notice(ctx, []Obligation{first}, now)
	_ = store.Credit(ctx, "episode:one", 0.6, now)
	_ = store.Notice(ctx, []Obligation{first}, now.Add(time.Minute))

	stored, _ := store.List(ctx, now.Add(time.Minute))
	if len(stored) != 1 || stored[0].Credit != 0.6 {
		t.Fatalf("re-noticing wiped the credit: %+v", stored)
	}
}

// An obligation that never got on air stops being news rather than queueing
// forever. The episode is not lost — it just goes back to being back catalogue.
func TestObligationsExpire(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	store := NewMemoryObligations()
	_ = store.Notice(ctx, []Obligation{
		obligation("episode:stale", TierB, now.Add(-71*time.Hour), 72*time.Hour),
	}, now)

	if pending, _ := store.List(ctx, now); len(pending) != 1 {
		t.Fatalf("should still be owed an hour before it expires")
	}
	if remaining, _ := store.List(ctx, now.Add(2*time.Hour)); len(remaining) != 0 {
		t.Fatalf("an expired obligation should drop out of the queue, got %+v", remaining)
	}
}

// ---- the engine's use of them -------------------------------------------

// An important episode dropping in the middle of the afternoon is owed at
// 13:37, not tomorrow morning. Nothing is interrupted; it goes out at the next
// boundary.
func TestAnEpisodeReleasedMidAfternoonIsOwedImmediately(t *testing.T) {
	afternoon := time.Date(2026, 8, 10, 13, 37, 0, 0, time.UTC)
	sources := []Source{
		podcastSource("pod1", "Daily Show", "p1"),
		musicSource("mus1", "House", "pl1"),
	}
	sources[0].Config["tier"] = "S"
	cat := &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{"p1": {
			episode("old", "Back catalogue", afternoon.Add(-40*24*time.Hour), 30),
		}},
		playlists: map[string][]catalog.MusicTrack{"pl1": {
			track("t1", "Song one", "Artist A", 200),
			track("t2", "Song two", "Artist B", 200),
		}},
	}
	s := newStation(t, twoCategoryPlan(0.75), sources, cat, afternoon)
	s.engine.Plan.Pools[0].SourceIDs = []string{"pod1"}

	// Nothing owed yet.
	if queue := s.env().owed; queue.Len() != 0 {
		t.Fatalf("nothing should be owed before the episode exists, got %d", queue.Len())
	}

	// It drops. Same catalog, one more entry.
	cat.episodes["p1"] = append(cat.episodes["p1"],
		episode("brandnew", "This afternoon's episode", afternoon.Add(-2*time.Minute), 35))

	queue := s.env().owed
	if queue.Len() != 1 || queue.Pending[0].ItemRef != "episode:brandnew" {
		t.Fatalf("the new episode should be owed the moment it appears, got %+v", queue.Pending)
	}

	item, decision := s.decide()
	if item.ItemRef != "episode:brandnew" {
		t.Fatalf("expected the new episode at the next boundary, got %q\n%s", item.Title, decision.Explain())
	}
	if decision.Selected == nil || !decision.Selected.Owed {
		t.Fatalf("the record should say this was surfaced because it was owed")
	}
}

// A block that asks for something owed plays only owed things — until there is
// nothing left, at which point the cycle is over.
func TestAFreshCycleWorksThroughWhatIsOwedThenHandsOver(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	sources := []Source{
		podcastSource("pod1", "Show One", "p1"),
		podcastSource("pod2", "Show Two", "p2"),
		musicSource("mus1", "House", "pl1"),
	}
	cat := &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{
			"p1": {
				episode("fresh1", "New one", now.Add(-3*time.Hour), 20),
				episode("archive1", "Old one", now.Add(-50*24*time.Hour), 20),
			},
			"p2": {
				episode("fresh2", "New two", now.Add(-4*time.Hour), 20),
				episode("archive2", "Old two", now.Add(-60*24*time.Hour), 20),
			},
		},
		playlists: map[string][]catalog.MusicTrack{"pl1": {
			track("t1", "Song one", "Artist A", 200),
			track("t2", "Song two", "Artist B", 200),
			track("t3", "Song three", "Artist C", 200),
		}},
	}

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 0.7}, {ID: "music", Target: 0.3}},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"pod1", "pod2"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
		},
		Blocks: []Block{
			{
				ID: "fresh-cycle", Label: "Fresh Podcasts",
				Enter:   BlockEntry{At: "09:00", Days: "*", When: "obligations.pending > 0"},
				Exit:    BlockExit{When: "obligations.pending == 0"},
				Pattern: []PatternStep{{Want: WantObligation}, {Want: WantBreak}},
				Pools:   []PoolRef{{Pool: "talk"}, {Pool: "music"}},
				Breaks: &BreakPolicy{
					Target:   BreakSize{Duration: "3m", Items: 1},
					Accept:   BreakRange{Items: []int{1, 2}, Duration: []string{"2m", "8m"}},
					Elements: []BreakElement{{Pool: "music", Count: []int{1, 2}, Fill: true}},
				},
				Next: "general",
			},
			{
				ID: "general", Label: "General rotation", Default: true,
				Pools: []PoolRef{{Pool: "talk"}, {Pool: "music"}},
			},
		},
	}
	s := newStation(t, plan, sources, cat, now)

	// New episode, break, new episode, break — then the cycle is out of things
	// it owes and the station moves on.
	order := []string{}
	for i := 0; i < 6; i++ {
		item := s.play()
		order = append(order, item.ItemRef)
	}
	if order[0] != "episode:fresh1" && order[0] != "episode:fresh2" {
		t.Fatalf("the cycle should open with something owed, got %q (%v)", order[0], order)
	}
	if order[1][:6] != "track:" {
		t.Fatalf("a break should follow the first new episode, got %q (%v)", order[1], order)
	}
	if order[2] != "episode:fresh1" && order[2] != "episode:fresh2" {
		t.Fatalf("the second new episode should follow the break, got %q (%v)", order[2], order)
	}
	if order[0] == order[2] {
		t.Fatalf("the cycle played the same episode twice: %v", order)
	}
	// By now everything owed has aired; the block's exit condition fires and it
	// hands over.
	if s.state.BlockID != "general" {
		t.Fatalf("with nothing left owed the cycle should hand over to general, still in %q (%v)",
			s.state.BlockID, order)
	}
}

// Pressing skip spends one surfacing, and the tier decides what that costs.
//
// Jacob's rule: "if I skip a podcast it should count for the listen credit —
// because if it's A or S tier maybe I just don't wanna listen to it in that
// moment, but I'll wanna listen to it. But if it's B tier or lower, I prolly
// don't wanna hear it again."
//
// That falls straight out of the credit model. A skip is one surfacing: a show
// worth two has one left and comes round again; a show worth one is settled.
func TestSkippingSpendsOneSurfacing(t *testing.T) {
	policy := FreshnessPolicy{Surfacings: map[string]int{"S": 2, "A": 2}}
	now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)

	skip := func(tier Tier) Obligation {
		o := Obligation{
			Tier: tier, State: ObligationPending,
			SettleAt:  policy.SurfacingsFor(tier),
			ExpiresAt: now.Add(48 * time.Hour),
		}
		// What the skip button does: credit one full surfacing.
		o.Credit += 1
		settle(&o, now)
		return o
	}

	// Top tier: skipped once, still owed — you said not now, not never.
	if got := skip(TierS); got.State != ObligationPending {
		t.Fatalf("skipping an S-tier episode settled it outright (%s); it should come round again", got.State)
	}
	if got := skip(TierA); got.State != ObligationPending {
		t.Fatalf("skipping an A-tier episode settled it outright (%s)", got.State)
	}

	// B and below: skipped means done.
	for _, tier := range []Tier{TierB, TierC, TierD} {
		if got := skip(tier); got.State != ObligationSatisfied {
			t.Fatalf("skipping a %s-tier episode left it owed (%s); it will come back and nag",
				tier, got.State)
		}
	}

	// And a SECOND skip finishes off even the top tier.
	twice := Obligation{Tier: TierS, State: ObligationPending,
		SettleAt: policy.SurfacingsFor(TierS), ExpiresAt: now.Add(48 * time.Hour)}
	twice.Credit += 1
	settle(&twice, now)
	twice.Credit += 1
	settle(&twice, now)
	if twice.State != ObligationSatisfied {
		t.Fatalf("skipping an S-tier episode twice still left it owed (%s)", twice.State)
	}
}
