package channels

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Monday's episodes arrive during an overnight relay, before the next actual
// decision. The owed read must see them without writing or resetting credit.
func TestOwedSnapshotDiscoversEpisodesBetweenPlaybackDecisions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 21, 7, 30, 0, 0, time.UTC)
	source := podcastSource("pod1", "Monday show", "p1")
	source.Config["tier"] = "S"
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{"p1": {
		episode("monday", "New Monday episode", now.Add(-time.Hour), 60),
		episode("partial", "Already aired once", now.Add(-24*time.Hour), 60),
		episode("settled", "Already finished", now.Add(-24*time.Hour), 60),
		episode("future", "Tomorrow", now.Add(24*time.Hour), 60),
		episode("old", "Old archive", now.Add(-30*24*time.Hour), 60),
	}}}
	s := newStation(t, twoCategoryPlan(0.75), []Source{source}, cat, now)
	s.engine.Plan.Pools[0].SourceIDs = []string{source.ID}
	stored := []Obligation{
		{ItemRef: "episode:partial", SourceID: source.ID, State: ObligationPending,
			PublishedAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(48 * time.Hour), Credit: 1, SettleAt: 2, Airings: 1},
		{ItemRef: "episode:settled", SourceID: source.ID, State: ObligationSatisfied,
			PublishedAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(48 * time.Hour), Credit: 2, SettleAt: 2},
		{ItemRef: "episode:removed", SourceID: "removed", State: ObligationPending,
			PublishedAt: now.Add(-time.Hour), ExpiresAt: now.Add(48 * time.Hour)},
	}
	_ = s.engine.Obligations.Notice(ctx, stored, now)
	before, _ := s.engine.Obligations.List(ctx, now)
	for range 2 {
		snapshot, current := s.engine.owedSnapshot(ctx, now, before)
		byRef := map[string]Obligation{}
		for _, item := range current {
			byRef[item.ItemRef] = item
		}
		if len(current) != 4 || !byRef["episode:monday"].Pending() {
			t.Fatalf("new episode missing or stale/future episode added: %+v", current)
		}
		if got := byRef["episode:partial"]; got.Credit != 1 || got.Airings != 1 || !got.Pending() {
			t.Fatalf("prior credit changed: %+v", got)
		}
		if byRef["episode:settled"].State != ObligationSatisfied {
			t.Fatal("settled episode was resurrected")
		}
		if _, ok := snapshot.JudgeOwed(ctx, now, s.state)["episode:monday"]; !ok {
			t.Fatal("new episode did not receive the scheduler's hold judgement")
		}
		after, _ := s.engine.Obligations.List(ctx, now)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("reading owed changed real obligations: before=%+v after=%+v", before, after)
		}
	}
}

func TestOwedServiceSeesUnscheduledEpisodesWithoutWriting(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustChannel(t, db, "ch1")
	mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourcePodcastSubscription, Label: "Monday show", Role: RoleTalk,
		Config: map[string]any{"podcastId": "p1", "tier": "S"}, Enabled: boolPtr(true),
	})
	now := time.Now().UTC()
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{"p1": {
		episode("monday", "Just imported", now.Add(-time.Hour), 60),
		episode("heard", "Finished on the phone", now.Add(-2*time.Hour), 60),
	}}}
	ears := newStationEars()
	ears.heardBySomeone("heard", 60*60)
	service := NewService(ServiceOptions{DB: db, Catalog: cat, Listened: ears})
	for range 2 {
		items, err := service.Owed(ctx, "ch1")
		if err != nil {
			t.Fatal(err)
		}
		byRef := map[string]Obligation{}
		for _, item := range items {
			byRef[item.ItemRef] = item
		}
		if got := byRef["episode:monday"]; !got.Pending() || got.SourceLabel != "Monday show" {
			t.Fatalf("new episode missing from the service response: %+v", items)
		}
		if byRef["episode:heard"].State != ObligationSatisfied {
			t.Fatalf("a finished episode should not be owed: %+v", items)
		}
		for _, table := range []string{"channel_obligations", "channel_program_state", "channel_decisions", "channel_play_log"} {
			var count int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("reading owed wrote %d rows to %s", count, table)
			}
		}
	}
}
