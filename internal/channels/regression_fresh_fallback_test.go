package channels

import (
	"context"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// WAN played 64m52s of its 151m18s on Saturday. At 10:42 Monday it still
// counted as unheard (credit 0.43), and its S tier beat untouched episodes.
func TestUntouchedEpisodesPrecedePartlyPlayedWAN(t *testing.T) {
	now := time.Date(2026, 9, 21, 16, 42, 30, 0, time.UTC)
	s := neverHeardStation(t, now)
	s.engine.Sources = []Source{
		podcastSource("wan", "The WAN Show", "wan"),
		podcastSource("cbb", "Comedy Bang Bang", "cbb"),
		podcastSource("history", "History of Everything", "history"),
	}
	for i, tier := range []string{"S", "A", "C"} {
		s.engine.Sources[i].Config["tier"] = tier
	}
	s.engine.Catalog = &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"wan":     {episode("wan", "I'm a Tesla Driver Now", now.Add(-61*time.Hour), 151)},
		"cbb":     {episode("cbb", "The Song of the Guyren", now.Add(-10*time.Hour), 82)},
		"history": {episode("history", "The Most Laidback Dictator in History", now.Add(-9*time.Hour), 35)},
	}}
	s.env()
	if err := s.engine.Obligations.Credit(context.Background(), "episode:wan", 3892.0/9078, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A gets one hearing and remains owed its second. C still goes next,
	// followed by the partly played WAN episode once the untouched queue ends.
	for _, want := range []string{"episode:cbb", "episode:history", "episode:wan"} {
		item, decision := s.step()
		if item.ItemRef != want {
			t.Fatalf("played %s, want %s\n%s", item.ItemRef, want, decision.Explain())
		}
	}
	queue := s.env().owed
	for _, ref := range []string{"episode:wan", "episode:cbb"} {
		owed, ok := queue.Get(ref)
		if !ok || !owed.Pending() || owed.Target() != 2 {
			t.Fatalf("%s should retain its remaining credit toward two surfacings: %+v", ref, owed)
		}
	}
	if queue.Owes("episode:history") {
		t.Fatal("the C-tier episode should settle after one hearing")
	}
}

func TestUntouchedOutranksPartialUnderAnyWeights(t *testing.T) {
	now := time.Date(2026, 9, 21, 16, 42, 30, 0, time.UTC)
	partial := Obligation{ItemRef: "partial-s", Tier: TierS, Credit: 0.43,
		State: ObligationPending, SettleAt: 2,
		PublishedAt: now.Add(-9 * time.Hour), ExpiresAt: now.Add(time.Hour)}
	untouched := Obligation{ItemRef: "untouched-f", Tier: TierF,
		State: ObligationPending, SettleAt: 1,
		PublishedAt: now.Add(-70 * time.Hour), ExpiresAt: now.Add(300 * time.Hour)}
	for _, policy := range []FreshnessPolicy{
		{},
		{TierSpread: 10, RecencyWeight: 4, ExpiryWeight: 12, UrgentFrom: 0.5},
		{TierSpread: 0.5, RecencyWeight: 3, ExpiryWeight: 3},
	} {
		queue := NewObligationQueue([]Obligation{partial, untouched}, now, policy)
		if queue.Pending[0].ItemRef != untouched.ItemRef {
			t.Fatalf("partly played S outranked untouched F under %+v", policy)
		}
		var decision Decision
		decision.applyOwed(queue, now, policy)
		if decision.Owed[0].Started || !decision.Owed[1].Started || decision.Owed[1].Heard {
			t.Fatalf("decision should distinguish untouched from partial: %+v", decision.Owed)
		}
	}
}

// A short repeat can score above a long first hearing. The obligation queue
// corrects that order, including when its first choice cannot be played.
func TestFreshEpisodesBeforeRepeatsAfterUnplayableChoice(t *testing.T) {
	for _, tc := range []struct {
		name       string
		unplayable int
		want       string
	}{
		{"fresh by tier", 0, "episode:fresh-a"},
		{"next fresh before repeat", 1, "episode:fresh-b"},
		{"repeat fills remaining time", 2, "episode:wan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
			plan := twoCategoryPlan(1)
			plan.Pools[0] = Pool{ID: "talk", Match: &PoolMatch{Kind: SourcePodcastSubscription}}
			plan.Freshness.Surfacings = map[string]int{"S": 2, "A": 2}
			plan.LongForm = LongFormPolicy{Threshold: "2h", Rest: "21d"}
			plan.Selection.Weights = map[string]float64{"commitment": 10}
			sources := []Source{
				podcastSource("wan", "WAN Show", "wan"),
				podcastSource("a", "Fresh A", "a"),
				podcastSource("b", "Fresh B", "b"),
			}
			for i, tier := range []string{"S", "A", "B"} {
				sources[i].Config["tier"] = tier
			}
			cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
				"wan": {episode("wan", "WAN replay", now.Add(-48*time.Hour), 60)},
				"a":   {episode("fresh-a", "New A episode", now.Add(-time.Hour), 180)},
				"b":   {episode("fresh-b", "New B episode", now.Add(-2*time.Hour), 180)},
			}}
			// A short archive establishes the station's normal episode length.
			for _, id := range []string{"wan", "a", "b"} {
				cat.episodes[id] = append(cat.episodes[id], episode(id+"-old", "Archive", now.Add(-30*24*time.Hour), 60))
			}
			for _, id := range []string{"a", "b"}[:tc.unplayable] {
				cat.episodes[id][0].AudioFiles = nil
			}
			s := newStation(t, plan, sources, cat, now)
			s.env()
			s.heardOnce("wan", "wan", time.Hour, now.Add(-24*time.Hour))
			item, decision := s.decide()
			if len(decision.Candidates) == 0 || decision.Candidates[0].Ref != "episode:wan" {
				t.Fatalf("fixture needs the repeat to lead by score\n%s", decision.Explain())
			}
			if item.ItemRef != tc.want {
				t.Fatalf("played %s, want %s\n%s", item.ItemRef, tc.want, decision.Explain())
			}
			for _, ref := range []string{"episode:fresh-a", "episode:fresh-b"}[:tc.unplayable] {
				if rule, _ := rejectionOf(decision, ref); rule != "unplayable" {
					t.Errorf("missing unplayable reason for %s\n%s", ref, decision.Explain())
				}
			}
		})
	}
}
