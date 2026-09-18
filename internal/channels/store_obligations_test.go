package channels

import (
	"context"
	"testing"
	"time"
)

// The obligation table against a real schema: what is read on every decision
// stays bounded, and what nothing will read again is pruned.
func TestSettledObligationsAgeOutOfTheListAndArePruned(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustChannel(t, db, "chan-owed")
	store := NewSQLObligations(db, "chan-owed")
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	notice := func(ref string, published time.Time) {
		t.Helper()
		if err := store.Notice(ctx, []Obligation{{
			SourceID: "src", ItemRef: ref, Title: ref, Tier: TierA,
			PublishedAt: published, ExpiresAt: published.Add(72 * time.Hour), SettleAt: 1,
		}}, published); err != nil {
			t.Fatal(err)
		}
	}
	settleAt := func(ref string, at time.Time) {
		t.Helper()
		if err := store.Credit(ctx, ref, 1, at); err != nil {
			t.Fatal(err)
		}
	}
	// Still owed.
	notice("episode:pending", now.Add(-time.Hour))
	// Settled yesterday: listed, so the screen can say why it is not offered.
	notice("episode:recent", now.Add(-26*time.Hour))
	settleAt("episode:recent", now.Add(-24*time.Hour))
	// Settled a fortnight ago: no longer listed, still kept.
	notice("episode:stale", now.AddDate(0, 0, -15))
	settleAt("episode:stale", now.AddDate(0, 0, -14))
	// Settled six weeks ago: nothing will ever read it again.
	notice("episode:ancient", now.AddDate(0, 0, -45))
	settleAt("episode:ancient", now.AddDate(0, 0, -44))

	listed, err := store.List(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]ObligationState{}
	for _, obligation := range listed {
		refs[obligation.ItemRef] = obligation.State
	}
	if refs["episode:pending"] != ObligationPending || refs["episode:recent"] != ObligationSatisfied {
		t.Fatalf("the pending and the recently settled rows should be listed, got %v", refs)
	}
	if _, ok := refs["episode:stale"]; ok {
		t.Fatalf("a row settled a fortnight ago is still being read on every decision: %v", refs)
	}

	pruned, err := PruneObligations(ctx, db, "chan-owed", now)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned %d rows, want just the ancient one", pruned)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_obligations WHERE channel_id = 'chan-owed'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 3 {
		t.Fatalf("%d rows remain, want 3 (pending, recent, stale)", remaining)
	}
}
