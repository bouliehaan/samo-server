package channels

import (
	"context"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// How a day is shaped: which block is on, what ends it, and what follows.

// morningPlan is the shape the brief describes — a booked news hour, a short
// music bridge after it, then general programming — written the way a station
// owner would: the follow-ons are anchored to the block BEFORE them, not to a
// clock.
func morningPlan(newsStart, newsEnd string) Plan {
	return Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		Pools: []Pool{
			{ID: "news", SourceIDs: []string{"news"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
			{ID: "talk", SourceIDs: []string{"pod1"}},
		},
		Blocks: []Block{
			{
				ID: "morning-news", Label: "Morning News",
				Enter: BlockEntry{At: newsStart, Days: "*", Hard: true, Start: StartMakeNext},
				Exit:  BlockExit{At: newsEnd},
				Pools: []PoolRef{{Pool: "news"}},
				Next:  "music-bridge",
			},
			{
				ID: "music-bridge", Label: "Music Bridge",
				Enter: BlockEntry{After: "morning-news"},
				Exit:  BlockExit{Duration: "12m", Tolerance: "6m"},
				Pools: []PoolRef{{Pool: "music"}},
				Next:  "general",
			},
			{
				ID: "general", Label: "General rotation", Default: true,
				Pools: []PoolRef{{Pool: "talk"}, {Pool: "music"}},
			},
		},
	}
}

// THE requirement: move the news and everything after it moves too, with no
// other edit. Under the old model there was nothing downstream to move — a slot
// was a slot — so re-timing a show meant re-timing every consequence of it by
// hand.
func TestMovingAnAnchorMovesWhatFollowsIt(t *testing.T) {
	check := func(t *testing.T, plan Plan, exitAt time.Time) {
		t.Helper()
		timeline := BuildTimeline(plan, exitAt, time.UTC)
		state := ProgramState{BlockID: "morning-news", EnteredAt: exitAt.Add(-time.Hour)}
		decision := ResolveBlock(plan, timeline, state, ConditionContext{}, exitAt)
		if decision.Block.ID != "music-bridge" {
			t.Fatalf("at %s the station should have handed over to the music bridge, got %q (%s)",
				exitAt.Format("15:04"), decision.Block.ID, decision.EntryReason)
		}
	}

	// Booked 07:00–08:00: the bridge starts just after 08:00.
	t.Run("news at 07:00", func(t *testing.T) {
		check(t, morningPlan("07:00", "08:00"), time.Date(2026, 8, 10, 8, 1, 0, 0, time.UTC))
	})
	// Move it to 06:30–07:30 and change NOTHING else.
	t.Run("news moved to 06:30", func(t *testing.T) {
		check(t, morningPlan("06:30", "07:30"), time.Date(2026, 8, 10, 7, 31, 0, 0, time.UTC))
	})
}

// A block with a duration ends when it has run, and hands over to its `next`.
func TestADurationBlockEndsAndHandsOver(t *testing.T) {
	plan := morningPlan("07:00", "08:00")
	entered := time.Date(2026, 8, 10, 8, 1, 0, 0, time.UTC)

	stillRunning := entered.Add(5 * time.Minute)
	timeline := BuildTimeline(plan, stillRunning, time.UTC)
	state := ProgramState{BlockID: "music-bridge", EnteredAt: entered}
	if decision := ResolveBlock(plan, timeline, state, ConditionContext{}, stillRunning); decision.Block.ID != "music-bridge" {
		t.Fatalf("five minutes into a twelve-minute block the station left it for %q", decision.Block.ID)
	}

	over := entered.Add(13 * time.Minute)
	timeline = BuildTimeline(plan, over, time.UTC)
	decision := ResolveBlock(plan, timeline, state, ConditionContext{}, over)
	if decision.Block.ID != "general" {
		t.Fatalf("after its twelve minutes the bridge should hand over to general, got %q", decision.Block.ID)
	}
}

// A block whose entry condition is false passes the hour on rather than leaving
// the station with nothing. The walk always terminates at the default block.
func TestHandoverSkipsABlockThatDeclines(t *testing.T) {
	plan := morningPlan("07:00", "08:00")
	// The bridge only wants to run when there is real room ahead of it.
	plan.Blocks[1].Enter.When = "window >= 2h"

	at := time.Date(2026, 8, 10, 8, 1, 0, 0, time.UTC)
	timeline := BuildTimeline(plan, at, time.UTC)
	state := ProgramState{BlockID: "morning-news", EnteredAt: at.Add(-time.Hour)}
	// Window of 30 minutes: the condition fails.
	decision := ResolveBlock(plan, timeline, state, ConditionContext{Window: 30 * time.Minute}, at)
	if decision.Block.ID != "general" {
		t.Fatalf("a declining block should pass the hour on to the default, got %q", decision.Block.ID)
	}
}

// A block whose pools have all gone dry hands over on its own. This is how "run
// the fresh queue until it is empty" terminates without anybody writing an exit
// condition for it.
func TestABlockWithNothingLeftToPlayHandsOver(t *testing.T) {
	plan := morningPlan("07:00", "08:00")
	at := time.Date(2026, 8, 10, 8, 5, 0, 0, time.UTC)
	timeline := BuildTimeline(plan, at, time.UTC)
	state := ProgramState{BlockID: "music-bridge", EnteredAt: at.Add(-time.Minute)}
	cond := ConditionContext{PoolAvailable: func(poolID string) bool { return poolID != "music" }}

	decision := ResolveBlock(plan, timeline, state, cond, at)
	if decision.Block.ID != "general" {
		t.Fatalf("a block with an empty pool should hand over, got %q (%s)", decision.Block.ID, decision.EntryReason)
	}
}

// Stickiness is what makes a block a block rather than a re-derivation at every
// item — and it is also what a restart has to preserve.
func TestABlockIsStickyAcrossDecisionsAndRestarts(t *testing.T) {
	plan := morningPlan("07:00", "08:00")
	entered := time.Date(2026, 8, 10, 8, 1, 0, 0, time.UTC)
	state := ProgramState{BlockID: "music-bridge", EnteredAt: entered, ItemCount: 2}

	for _, offset := range []time.Duration{time.Minute, 3 * time.Minute, 8 * time.Minute} {
		at := entered.Add(offset)
		decision := ResolveBlock(plan, BuildTimeline(plan, at, time.UTC), state, ConditionContext{}, at)
		if decision.Block.ID != "music-bridge" {
			t.Fatalf("+%s: left the block early for %q", offset, decision.Block.ID)
		}
		if !decision.EnteredAt.Equal(entered) {
			t.Fatalf("+%s: entered-at moved to %s; a restart would restart the block", offset, decision.EnteredAt)
		}
		if decision.Changed {
			t.Fatalf("+%s: reported a block change when nothing changed", offset)
		}
	}
}

// The default block accepts everything, always. That plus plan validation
// rejecting a fallback chain with no default is the guarantee the station has
// somewhere to be.
func TestTheDefaultBlockIsAlwaysAvailable(t *testing.T) {
	plan := morningPlan("07:00", "08:00")
	at := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	decision := ResolveBlock(plan, BuildTimeline(plan, at, time.UTC), ProgramState{}, ConditionContext{}, at)
	if decision.Block.ID != "general" {
		t.Fatalf("mid-afternoon with nothing booked the default block should be on, got %q", decision.Block.ID)
	}
}

// A booked slot outranks whatever the station was doing, and its own window is
// what ends it.
func TestAnAnchorTakesOverAndReleasesOnTime(t *testing.T) {
	plan := morningPlan("07:00", "08:00")
	inside := time.Date(2026, 8, 10, 7, 20, 0, 0, time.UTC)
	state := ProgramState{BlockID: "general", EnteredAt: inside.Add(-2 * time.Hour)}

	decision := ResolveBlock(plan, BuildTimeline(plan, inside, time.UTC), state, ConditionContext{}, inside)
	if decision.Block.ID != "morning-news" {
		t.Fatalf("the booked hour should be on air at 07:20, got %q", decision.Block.ID)
	}
	if decision.Anchor == nil {
		t.Fatalf("the decision should carry the anchor that put it on air")
	}

	after := time.Date(2026, 8, 10, 8, 2, 0, 0, time.UTC)
	held := ProgramState{BlockID: "morning-news", EnteredAt: inside}
	next := ResolveBlock(plan, BuildTimeline(plan, after, time.UTC), held, ConditionContext{}, after)
	if next.Block.ID != "music-bridge" {
		t.Fatalf("after its window the booked block should release, got %q", next.Block.ID)
	}
}

// ---- balance over time -------------------------------------------------

// One long item pushes its category into surplus and the other simply wins the
// next few picks. That is the self-correction working, not a fault.
func TestOneLongItemDoesNotLockOutEitherCategory(t *testing.T) {
	sources, cat, now := twoPodcastsAndMusic()
	cat.episodes["p1"] = []catalog.PodcastEpisode{
		episode("long", "Three hours", now.Add(-40*24*time.Hour), 180),
		episode("short", "Half an hour", now.Add(-41*24*time.Hour), 30),
	}
	s := newStation(t, twoCategoryPlan(0.75), sources, cat, now)
	s.aired("pod1", "talk", 3*time.Hour)

	seen := map[CategoryID]bool{}
	for i := 0; i < 8; i++ {
		seen[s.play().Category] = true
	}
	if !seen["music"] || !seen["talk"] {
		t.Fatalf("after a three-hour item the station should recover both categories, saw %v", seen)
	}
}

// Being passed over is precisely what earns a source its next turn, so nothing
// can be starved — not music, and not the podcast whose last episode was 2019.
func TestAnIgnoredSourceEventuallyWins(t *testing.T) {
	sources, cat, now := twoPodcastsAndMusic()
	s := newStation(t, twoCategoryPlan(0.75), sources, cat, now)
	// pod1 has had the whole talk share for hours; pod2 has had nothing.
	s.aired("pod1", "talk", 4*time.Hour)

	for i := 0; i < 8; i++ {
		if s.play().SourceID == "pod2" {
			return
		}
	}
	t.Fatalf("a source ignored for four hours never came up in eight picks")
}

// An hour of booked programming is an hour of that category whether or not a
// slot picked it, so it has to push what comes after it the other way.
// Otherwise the schedule and the rotation each behave sensibly on their own and
// the day adds up to nonsense.
func TestScheduledProgrammingCountsTowardTheBalance(t *testing.T) {
	sources, cat, now := twoPodcastsAndMusic()
	s := newStation(t, twoCategoryPlan(0.75), sources, cat, now)
	// A booked talk hour, recorded exactly as the streamer records one.
	s.history.Record(MemoryPlay{
		SourceID: "pod1", ItemRef: "episode:booked", Category: "talk",
		StartedAt: now.Add(-5 * time.Hour), EndedAt: now.Add(-time.Hour),
		DurationSeconds: 4 * 60 * 60,
	})

	scoring := s.engine.scoreEnv(context.Background(), s.now, ProgrammingIntent{
		Targets: map[CategoryID]float64{"talk": 0.75, "music": 0.25},
	}, nil)
	if scoring.airtime.ByCategory["talk"] < 3*time.Hour {
		t.Fatalf("booked programming did not reach the balance: talk = %s", scoring.airtime.ByCategory["talk"])
	}
	if scoring.categoryDeficit("talk") >= scoring.categoryDeficit("music") {
		t.Fatalf("after four booked hours of talk, music should be the more owed category")
	}
}

// ---- history arithmetic ------------------------------------------------

// The regression from 2026-08-09: an eight-hour block that began nine hours ago
// is still five hours of a six-hour window, but `started_at > cutoff` loses it
// entirely — so the balance reads "no talk lately" at the precise moment the
// station has been doing nothing else.
func TestAirtimeCountsOverlapNotJustStarts(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	history := NewMemoryHistory()
	history.Record(MemoryPlay{
		SourceID: "pod1", ItemRef: "episode:overnight", Category: "talk",
		StartedAt: now.Add(-9 * time.Hour), EndedAt: now.Add(-1 * time.Hour),
	})

	window, err := history.Airtime(context.Background(), 6*time.Hour, now)
	if err != nil {
		t.Fatalf("airtime: %v", err)
	}
	if got := window.ByCategory["talk"]; got < 4*time.Hour || got > 5*time.Hour {
		t.Fatalf("talk airtime in a 6h window = %s, want ~5h of the overlapping block", got)
	}
}

// An item still on air counts up to now, so a long item in progress pushes the
// balance immediately rather than only once it finishes.
func TestAnItemStillPlayingCountsSoFar(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	history := NewMemoryHistory()
	history.Record(MemoryPlay{
		SourceID: "pod1", ItemRef: "episode:running", Category: "talk",
		StartedAt: now.Add(-2 * time.Hour),
	})
	window, _ := history.Airtime(context.Background(), 6*time.Hour, now)
	if got := window.ByCategory["talk"]; got < 119*time.Minute || got > 121*time.Minute {
		t.Fatalf("an in-progress item should count its two hours so far, got %s", got)
	}
}
