package channels

import (
	"testing"
	"time"
)

// The timeline is what the station knows about its own future. The old version
// could see thirty minutes ahead, today only, and only at slots that had not
// started yet — so a four-hour block could be started at 23:50 in front of a
// 06:00 appointment nobody could see.

func anchoredPlan(at, until, days string) Plan {
	return Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{"a"}}, {ID: "show", SourceIDs: []string{"b"}}},
		Blocks: []Block{
			{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{
				ID: "show", Label: "The Show",
				Enter: BlockEntry{At: at, Days: days, Hard: true, Start: StartMakeNext},
				Exit:  BlockExit{At: until},
				Pools: []PoolRef{{Pool: "show"}},
			},
		},
	}
}

func TestTimelineSeesTomorrowsAppointment(t *testing.T) {
	plan := anchoredPlan("06:00", "07:00", "*")
	lateAtNight := time.Date(2026, 8, 10, 23, 50, 0, 0, time.UTC)
	timeline := BuildTimeline(plan, lateAtNight, time.UTC)

	if timeline.Next == nil {
		t.Fatalf("at 23:50 the next appointment is tomorrow morning, and it must be visible")
	}
	if got := timeline.Next.Start.Format("2006-01-02 15:04"); got != "2026-08-11 06:00" {
		t.Fatalf("next anchor at %s, want tomorrow at 06:00", got)
	}
	if window := timeline.Window(); window < 6*time.Hour || window > 6*time.Hour+11*time.Minute {
		t.Fatalf("window = %s, want about 6h10m", window)
	}
}

func TestTimelineReportsTheActiveAnchor(t *testing.T) {
	plan := anchoredPlan("16:00", "17:00", "*")
	inside := time.Date(2026, 8, 10, 16, 30, 0, 0, time.UTC)
	timeline := BuildTimeline(plan, inside, time.UTC)
	if timeline.Active == nil || timeline.Active.BlockID != "show" {
		t.Fatalf("the show should be active at 16:30, got %+v", timeline.Active)
	}
	if timeline.Window() != 0 {
		// The next appointment is tomorrow's occurrence; the window is measured
		// to it, so it should be substantial rather than zero.
		if timeline.Window() < 20*time.Hour {
			t.Fatalf("window during an active anchor = %s", timeline.Window())
		}
	}
}

func TestAnchorHonoursItsWeekdays(t *testing.T) {
	plan := anchoredPlan("16:00", "17:00", "sat,sun")
	monday := time.Date(2026, 8, 10, 16, 30, 0, 0, time.UTC) // a Monday
	if timeline := BuildTimeline(plan, monday, time.UTC); timeline.Active != nil {
		t.Fatalf("a weekend-only show should not be on air on Monday")
	}
	saturday := time.Date(2026, 8, 15, 16, 30, 0, 0, time.UTC)
	if timeline := BuildTimeline(plan, saturday, time.UTC); timeline.Active == nil {
		t.Fatalf("a weekend show should be on air on Saturday")
	}
}

// A window that crosses midnight is a normal way to programme a night block.
func TestAnchorWindowCanCrossMidnight(t *testing.T) {
	plan := anchoredPlan("23:00", "02:00", "*")
	justAfterMidnight := time.Date(2026, 8, 11, 0, 30, 0, 0, time.UTC)
	timeline := BuildTimeline(plan, justAfterMidnight, time.UTC)
	if timeline.Active == nil {
		t.Fatalf("a 23:00–02:00 window should still be on air at 00:30")
	}
	if got := timeline.Active.End.Format("2006-01-02 15:04"); got != "2026-08-11 02:00" {
		t.Fatalf("window ends at %s, want 02:00 the next day", got)
	}
}

// An anchor with no stated end runs until the next one begins, which is what a
// booked slot with no end means on paper.
func TestAnchorWithNoEndRunsUntilTheNextOne(t *testing.T) {
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{"a"}}},
		Blocks: []Block{
			{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "first", Enter: BlockEntry{At: "08:00", Days: "*", Hard: true}, Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "second", Enter: BlockEntry{At: "12:00", Days: "*", Hard: true}, Pools: []PoolRef{{Pool: "talk"}}},
		},
	}
	at := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	timeline := BuildTimeline(plan, at, time.UTC)
	if timeline.Active == nil || timeline.Active.BlockID != "first" {
		t.Fatalf("the 08:00 block should be on air at 09:00, got %+v", timeline.Active)
	}
	if got := timeline.Active.End.Format("15:04"); got != "12:00" {
		t.Fatalf("an anchor with no end should run to the next one at 12:00, got %s", got)
	}
}

// Wall-clock times are built with time.Date, not by adding minutes to midnight.
// On a day the clock changes those are an hour apart, and the old code made
// exactly that mistake in three places — which is how a show fires an hour late
// twice a year and nobody can reproduce it.
func TestAnchorsSurviveADaylightSavingChange(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("no tzdata for America/Denver: %v", err)
	}
	plan := anchoredPlan("09:00", "10:00", "*")

	for _, day := range []struct {
		name string
		at   time.Time
	}{
		{"spring forward", time.Date(2026, 3, 8, 6, 0, 0, 0, denver)},
		{"fall back", time.Date(2026, 11, 1, 6, 0, 0, 0, denver)},
	} {
		timeline := BuildTimeline(plan, day.at, denver)
		if timeline.Next == nil {
			t.Fatalf("%s: no upcoming anchor", day.name)
		}
		if got := timeline.Next.Start.Format("15:04"); got != "09:00" {
			t.Fatalf("%s: anchor fires at %s, want 09:00 local", day.name, got)
		}
		if timeline.Next.Start.Day() != day.at.Day() {
			t.Fatalf("%s: anchor jumped to %s", day.name, timeline.Next.Start.Format("2006-01-02"))
		}
	}
}

// UTC shifts the WEEKDAY, not just the hour — which is how a Saturday 23:00
// slot looks "not booked today" to a server whose clock is UTC on purpose.
func TestAnchorsMatchWallClockNotUTC(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	plan := anchoredPlan("23:00", "23:59", "sat")
	// Saturday 23:30 in Denver is Sunday 05:30 UTC.
	at := time.Date(2026, 8, 15, 23, 30, 0, 0, denver)
	if at.UTC().Weekday() != time.Sunday {
		t.Fatalf("test premise wrong: %s in UTC", at.UTC().Weekday())
	}
	timeline := BuildTimeline(plan, at, denver)
	if timeline.Active == nil {
		t.Fatalf("a Saturday 23:00 slot must be on air at Saturday 23:30 local, whatever UTC thinks")
	}
}

func TestNonAnchorBlocksAreNotOnTheTimeline(t *testing.T) {
	plan := anchoredPlan("16:00", "17:00", "*")
	plan.Blocks[1].Enter.Hard = false
	at := time.Date(2026, 8, 10, 16, 30, 0, 0, time.UTC)
	timeline := BuildTimeline(plan, at, time.UTC)
	if len(timeline.Anchors) != 0 {
		t.Fatalf("only hard-anchored blocks are appointments, got %d", len(timeline.Anchors))
	}
	// A soft daypart still runs — it is just not something to programme around.
	decision := ResolveBlock(plan, timeline, ProgramState{}, ConditionContext{}, at)
	if decision.Block.ID != "show" {
		t.Fatalf("a soft daypart covering now should be on air, got %q", decision.Block.ID)
	}
}
