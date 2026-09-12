package channels

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The decision record under the one thing that used to destroy it: a station
// asking the same question every thirty seconds for hours because the answer
// produces no audio.

func decisionAt(at time.Time, blockID, ref string) Decision {
	d := Decision{At: at, ChannelID: "ch1", BlockID: blockID, BlockLabel: blockID, Considered: 1}
	if ref != "" {
		d.Selected = &SelectedSummary{Ref: ref, Title: ref, Reason: "booked"}
	}
	return d
}

func mustSaveDecision(t *testing.T, db *sql.DB, decision Decision) {
	t.Helper()
	if err := SaveDecision(context.Background(), db, "ch1", decision); err != nil {
		t.Fatalf("save decision at %s: %v", decision.At.Format(time.RFC3339), err)
	}
}

func mustRecentDecisions(t *testing.T, db *sql.DB) []Decision {
	t.Helper()
	decisions, err := RecentDecisions(context.Background(), db, "ch1", 50)
	if err != nil {
		t.Fatalf("recent decisions: %v", err)
	}
	return decisions
}

func countDecisions(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM channel_decisions WHERE channel_id = ?`, "ch1").Scan(&n); err != nil {
		t.Fatalf("count decisions: %v", err)
	}
	return n
}

// The same choice, made again every half minute, is one record that says so.
func TestARepeatedDecisionIsOneRecordThatCountsItsRetries(t *testing.T) {
	db := newTestDB(t)
	mustChannel(t, db, "ch1")
	first := time.Date(2026, 9, 9, 10, 56, 0, 0, time.UTC)

	at := first
	for i := 0; i < 5; i++ {
		mustSaveDecision(t, db, decisionAt(at, "slot-lofi", "station:lofi"))
		at = at.Add(30 * time.Second)
	}
	last := at.Add(-30 * time.Second)

	decisions := mustRecentDecisions(t, db)
	if len(decisions) != 1 {
		t.Fatalf("five retries of one choice should be one record, got %d", len(decisions))
	}
	run := decisions[0]
	if run.Retries == nil || run.Retries.Count != 4 {
		t.Fatalf("the record should count four retries, got %+v", run.Retries)
	}
	if !run.Retries.Since.Equal(first) {
		t.Fatalf("the run should date from the first choice %s, got %s", first, run.Retries.Since)
	}
	if !run.At.Equal(last) {
		t.Fatalf("the record should be stamped with the latest retry %s, got %s", last, run.At)
	}
	if run.Selected == nil || run.Selected.Ref != "station:lofi" {
		t.Fatalf("the record lost its selection: %+v", run.Selected)
	}

	// Coalescing is only ever into the newest row, and only for the same
	// choice. Something else playing, then the station again, is two more
	// decisions — that is the outage ending and beginning, and it should read
	// that way.
	mustSaveDecision(t, db, decisionAt(at, "slot-lofi", "track:fallback"))
	mustSaveDecision(t, db, decisionAt(at.Add(time.Minute), "slot-lofi", "station:lofi"))
	decisions = mustRecentDecisions(t, db)
	if len(decisions) != 3 {
		t.Fatalf("a different choice and a return to the first should be two new records, got %d", len(decisions))
	}
	if decisions[0].Retries != nil || decisions[1].Retries != nil {
		t.Fatalf("fresh decisions should carry no retry count: %+v / %+v", decisions[0].Retries, decisions[1].Retries)
	}
	if decisions[2].Retries == nil || decisions[2].Retries.Count != 4 {
		t.Fatalf("the earlier run should survive intact, got %+v", decisions[2].Retries)
	}
}

// A choice that comes round again after a while is a new decision, and the
// same item chosen from a different block is too.
func TestOnlyACloseRepeatInTheSameBlockCoalesces(t *testing.T) {
	db := newTestDB(t)
	mustChannel(t, db, "ch1")
	at := time.Date(2026, 9, 9, 10, 56, 0, 0, time.UTC)

	mustSaveDecision(t, db, decisionAt(at, "slot-lofi", "station:lofi"))
	mustSaveDecision(t, db, decisionAt(at.Add(decisionRepeatWindow+time.Second), "slot-lofi", "station:lofi"))
	if got := mustRecentDecisions(t, db); len(got) != 2 {
		t.Fatalf("the same choice outside the window is a new decision, got %d records", len(got))
	}

	// Inside the window, but the plan moved on to a block that happens to reach
	// for the same item: a different decision.
	mustSaveDecision(t, db, decisionAt(at.Add(decisionRepeatWindow+2*time.Second), "general", "station:lofi"))
	if got := mustRecentDecisions(t, db); len(got) != 3 {
		t.Fatalf("the same item from another block is a new decision, got %d records", len(got))
	}

	// A clock that went backwards is not a retry of anything.
	mustSaveDecision(t, db, decisionAt(at, "general", "station:lofi"))
	if got := mustRecentDecisions(t, db); len(got) != 4 {
		t.Fatalf("a decision stamped earlier than the newest is not a repeat of it, got %d records", len(got))
	}
}

// Selecting nothing repeats too — and faster: the streamer retries a scheduler
// error every five seconds, which is a worse flood than a dead source.
func TestRepeatedFailuresToChooseAreOneRecord(t *testing.T) {
	db := newTestDB(t)
	mustChannel(t, db, "ch1")
	at := time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC)

	for i := 0; i < 12; i++ {
		failed := decisionAt(at, "overnight", "")
		failed.Error = "nothing in the pool fits before the next booked slot"
		mustSaveDecision(t, db, failed)
		at = at.Add(5 * time.Second)
	}
	decisions := mustRecentDecisions(t, db)
	if len(decisions) != 1 || decisions[0].Retries == nil || decisions[0].Retries.Count != 11 {
		t.Fatalf("a minute of the same failure should be one record with eleven retries, got %+v", decisions)
	}

	// A different failure is a different fact.
	other := decisionAt(at, "overnight", "")
	other.Error = "channel has no enabled sources"
	mustSaveDecision(t, db, other)
	if got := mustRecentDecisions(t, db); len(got) != 2 {
		t.Fatalf("a different error is a new decision, got %d records", len(got))
	}
}

// A caller cannot pre-load a run: the count is the store's observation.
func TestARetryCountIsNeverTakenFromTheCaller(t *testing.T) {
	db := newTestDB(t)
	mustChannel(t, db, "ch1")
	at := time.Date(2026, 9, 9, 10, 56, 0, 0, time.UTC)
	forged := decisionAt(at, "slot-lofi", "station:lofi")
	forged.Retries = &RetrySummary{Count: 99, Since: at.Add(-time.Hour)}
	mustSaveDecision(t, db, forged)
	if got := mustRecentDecisions(t, db); got[0].Retries != nil {
		t.Fatalf("a first decision should carry no retry count, got %+v", got[0].Retries)
	}
}

// What the record is for: the row for last Thursday's complaint is still there
// after a flood of distinct decisions that would have pushed it out under a
// count-based cap, and rows older than the retention are gone.
func TestRetentionIsByAgeAndAFloodDoesNotEvictRecentHistory(t *testing.T) {
	db := newTestDB(t)
	mustChannel(t, db, "ch1")
	now := time.Date(2026, 9, 9, 10, 56, 0, 0, time.UTC)

	mustSaveDecision(t, db, decisionAt(now.Add(-8*24*time.Hour), "general", "track:stale"))
	complaint := decisionAt(now.Add(-5*24*time.Hour), "general", "track:complained-about")
	mustSaveDecision(t, db, complaint)

	// Distinct choices every thirty seconds for hours: alternating dead
	// stations, say, which the coalescing cannot fold. More rows than the old
	// cap of four hundred allowed.
	at := now
	for i := 0; i < 450; i++ {
		mustSaveDecision(t, db, decisionAt(at, "slot-lofi", fmt.Sprintf("station:dead-%d", i%2)))
		at = at.Add(30 * time.Second)
	}

	var stale, kept int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM channel_decisions WHERE channel_id = ? AND selected_ref = ?`,
		"ch1", "track:stale").Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM channel_decisions WHERE channel_id = ? AND selected_ref = ?`,
		"ch1", "track:complained-about").Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("a decision older than the retention should have been pruned")
	}
	if kept != 1 {
		t.Fatalf("the five-day-old decision should have survived the flood, got %d rows", kept)
	}
	if total := countDecisions(t, db); total != 451 {
		t.Fatalf("expected the flood plus the complaint, got %d rows", total)
	}
}

// The ceiling is a backstop against a runaway writer, not the budget: it only
// bites once a channel has written more than it inside the retention.
func TestTheCeilingStillBoundsARunawayWriter(t *testing.T) {
	db := newTestDB(t)
	mustChannel(t, db, "ch1")
	now := time.Date(2026, 9, 9, 10, 56, 0, 0, time.UTC)

	// Seeded in one statement rather than one save at a time: this is about
	// the prune, and the prune only needs the rows to exist.
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO channel_decisions (id, channel_id, decided_at, block_id, selected_ref, decision_json)
		SELECT 'cdec_seed_' || n, ?, ?, 'general', 'track:' || n, '{}'
		FROM generate_series(1, ?) AS n`,
		"ch1", now.Add(-time.Hour).Format(time.RFC3339), decisionCeiling+200); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mustSaveDecision(t, db, decisionAt(now, "general", "track:latest"))
	if total := countDecisions(t, db); total != decisionCeiling {
		t.Fatalf("expected the ceiling of %d rows, got %d", decisionCeiling, total)
	}
	if got := mustRecentDecisions(t, db); got[0].Selected == nil || got[0].Selected.Ref != "track:latest" {
		t.Fatalf("the newest decision should be the one kept at the front, got %+v", got[0].Selected)
	}
}

// The incident, end to end through the scheduler: a booked internet station
// that produces no audio, the streamer's failure path run against it for a
// while, and the record afterwards.
//
// 2026-09-09, "Lofi Weekday": unreachable from 10:56Z to 14:00Z, three hundred
// and fifty identical decision rows, and five days of history pruned to make
// room for them.
func TestAnUnreachableBookedStationDoesNotFloodTheRecord(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustChannel(t, db, "ch1")
	rotation := mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourcePodcastSubscription, Label: "Rotation", Role: RoleTalk,
		Config: map[string]any{"podcastId": "p1"}, Enabled: boolPtr(true),
	})
	lofi := mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourceInternetStation, Label: "Lofi Weekday", Role: RoleShow,
		Config: map[string]any{"stationId": "st-lofi"}, Enabled: boolPtr(true),
	})
	if _, err := InsertScheduleRule(ctx, db, "ch1", CreateScheduleRuleInput{
		SourceID: lofi.ID, Label: "Lofi Weekday", WeekdayMask: 127,
		StartMinute: 10 * 60, EndMinute: 14 * 60, Enabled: boolPtr(true),
	}); err != nil {
		t.Fatalf("insert rule: %v", err)
	}

	first := time.Date(2026, 9, 9, 10, 56, 0, 0, time.UTC)
	now := first
	skips := NewSkipRegistry(func() time.Time { return now })
	sched := NewScheduler(Dependencies{
		DB: db, Now: func() time.Time { return now }, Skips: skips,
		InternetStations: &stubInternetStations{station: InternetStation{
			ID: "st-lofi", Name: "Lofi Weekday", StreamURL: "http://lofi.example.test/live",
		}},
		Catalog: &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
			"p1": {episode("rot", "Rotation item", first.Add(-40*24*time.Hour), 30)},
		}},
	})

	const attempts = 20
	for attempt := 0; attempt < attempts; attempt++ {
		prior, _ := LoadProgramState(ctx, db, "ch1")
		item, err := sched.NextItem(ctx, "ch1")
		if err != nil {
			t.Fatalf("attempt %d: next item: %v", attempt, err)
		}
		if item.SourceID != lofi.ID {
			t.Fatalf("attempt %d: the booked station should be what keeps being chosen, got %q (rotation is %s)",
				attempt, item.Title, rotation.ID)
		}
		// What the streamer does when the item produces no audio: pass it
		// over, put the cycle back, step off the source after a few in a row,
		// back off, ask again.
		skips.SuppressRef(item.ItemRef)
		if prior.BlockID != "" {
			if err := SaveProgramState(ctx, db, "ch1", prior); err != nil {
				t.Fatal(err)
			}
		}
		if attempt+1 >= deadSourceAfter {
			skips.Suppress(lofi.ID, DefaultSkipSuppression)
		}
		now = now.Add(failureBackoff(attempt + 1))
	}

	decisions := mustRecentDecisions(t, db)
	if len(decisions) != 1 {
		t.Fatalf("%d retries of the same booked station should be one record, got %d", attempts, len(decisions))
	}
	run := decisions[0]
	if run.Selected == nil || run.Selected.Ref != "station:st-lofi" {
		t.Fatalf("the record should still say what was chosen, got %+v", run.Selected)
	}
	if run.Retries == nil || run.Retries.Count != attempts-1 {
		t.Fatalf("the record should count %d retries, got %+v", attempts-1, run.Retries)
	}
	if !run.Retries.Since.Equal(first) {
		t.Fatalf("the run should date from %s, got %s", first, run.Retries.Since)
	}
	if run.At.Equal(first) {
		t.Fatalf("the record should be stamped with the latest retry, not the first")
	}
	if len(run.Relaxed) == 0 {
		t.Fatalf("the latest account should show the skip rule being relaxed to reach the same answer, got %+v", run)
	}

	// The slot ends, something else plays, and the outage is still one row
	// behind it rather than the whole record.
	now = time.Date(2026, 9, 9, 14, 1, 0, 0, time.UTC)
	item, err := sched.NextItem(ctx, "ch1")
	if err != nil {
		t.Fatalf("after the slot: %v", err)
	}
	if item.SourceID != rotation.ID {
		t.Fatalf("after the slot the rotation should be back, got %q", item.Title)
	}
	decisions = mustRecentDecisions(t, db)
	if len(decisions) != 2 {
		t.Fatalf("expected the rotation's decision in front of the outage, got %d records", len(decisions))
	}
	if decisions[0].Retries != nil {
		t.Fatalf("the rotation's decision is not a retry of anything: %+v", decisions[0].Retries)
	}
	if decisions[1].Retries == nil || decisions[1].Retries.Count != attempts-1 {
		t.Fatalf("the outage record should be intact behind it, got %+v", decisions[1].Retries)
	}
}
