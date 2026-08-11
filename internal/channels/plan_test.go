package channels

import (
	"strings"
	"testing"
	"time"
)

// Plan validation is the load-bearing safety net: a plan that names a pool
// which does not exist, or whose blocks hand over in a loop, or that has no
// default block to fall back to, would take the station off the air at some
// unpredictable hour. Every one of those is rejected at save time with all the
// problems listed at once.

func validPlan() Plan {
	return Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{"a"}}, {ID: "music", SourceIDs: []string{"b"}}},
		Blocks: []Block{{
			ID: "general", Default: true,
			Pools: []PoolRef{{Pool: "talk"}, {Pool: "music"}},
		}},
	}
}

func TestValidPlanValidates(t *testing.T) {
	if err := validPlan().Validate(); err != nil {
		t.Fatalf("a plain plan should validate: %v", err)
	}
}

func TestPlanNeedsExactlyOneDefaultBlock(t *testing.T) {
	none := validPlan()
	none.Blocks[0].Default = false
	if err := none.Validate(); err == nil || !strings.Contains(err.Error(), "nowhere to fall back") {
		t.Fatalf("a plan with no default block must be rejected, got %v", err)
	}

	two := validPlan()
	two.Blocks = append(two.Blocks, Block{ID: "other", Default: true, Pools: []PoolRef{{Pool: "talk"}}})
	if err := two.Validate(); err == nil || !strings.Contains(err.Error(), "there can be only one") {
		t.Fatalf("two default blocks is an ambiguity, got %v", err)
	}
}

// The default block is where everything lands, so it cannot have a condition on
// getting there — and it needs something to play.
func TestDefaultBlockMustBeUnconditionalAndStocked(t *testing.T) {
	conditional := validPlan()
	conditional.Blocks[0].Enter.At = "07:00"
	if err := conditional.Validate(); err == nil || !strings.Contains(err.Error(), "no entry condition") {
		t.Fatalf("a conditional default block must be rejected, got %v", err)
	}

	empty := validPlan()
	empty.Blocks[0].Pools = nil
	if err := empty.Validate(); err == nil || !strings.Contains(err.Error(), "nothing to fall back on") {
		t.Fatalf("a default block with no pools must be rejected, got %v", err)
	}
}

func TestPlanRejectsUnknownReferences(t *testing.T) {
	plan := validPlan()
	plan.Blocks = append(plan.Blocks, Block{
		ID: "evening", Pools: []PoolRef{{Pool: "nope"}},
		Enter: BlockEntry{After: "ghost"}, Next: "vanished",
		Balance: map[CategoryID]float64{"jazz": 0.5},
	})
	err := plan.Validate()
	if err == nil {
		t.Fatalf("expected the unknown references to be rejected")
	}
	for _, want := range []string{"unknown pool", "unknown block", "unknown category"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q, got %v", want, err)
		}
	}
}

// A loop in the handover chain would make the fallback walk run forever inside
// a live streamer.
func TestPlanRejectsAHandoverLoop(t *testing.T) {
	plan := validPlan()
	plan.Blocks = append(plan.Blocks,
		Block{ID: "a", Pools: []PoolRef{{Pool: "talk"}}, Next: "b"},
		Block{ID: "b", Pools: []PoolRef{{Pool: "talk"}}, Next: "a"},
	)
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("a handover loop must be rejected, got %v", err)
	}
}

func TestPlanRejectsNonsenseTimesAndDurations(t *testing.T) {
	plan := validPlan()
	plan.Blocks = append(plan.Blocks, Block{
		ID: "bad", Pools: []PoolRef{{Pool: "talk"}},
		Enter: BlockEntry{At: "25:99", Days: "funday", Start: "whenever"},
		Exit:  BlockExit{Duration: "soonish"},
	})
	err := plan.Validate()
	if err == nil {
		t.Fatalf("expected the malformed fields to be rejected")
	}
	for _, want := range []string{"enter.at", "enter.days", "start policy", "exit.duration"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q, got %v", want, err)
		}
	}
}

func TestParsePlanRejectsUnknownFields(t *testing.T) {
	raw := `{"version":1,"categories":[{"id":"talk","target":1}],
	         "pools":[{"id":"p","sourceIds":["a"]}],
	         "blocks":[{"id":"g","default":true,"pools":[{"pool":"p"}]}],
	         "wat":true}`
	if _, err := ParsePlan([]byte(raw)); err == nil {
		t.Fatalf("a typo'd field should be an error, not silently ignored")
	}
}

func TestParsePlanRejectsAFutureVersion(t *testing.T) {
	raw := `{"version":99,"categories":[{"id":"talk","target":1}],
	         "pools":[{"id":"p","sourceIds":["a"]}],
	         "blocks":[{"id":"g","default":true,"pools":[{"pool":"p"}]}]}`
	if _, err := ParsePlan([]byte(raw)); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("a newer plan version should be refused rather than half-understood, got %v", err)
	}
}

// ---- category targets --------------------------------------------------

func TestCategoryTargetsNormaliseAndAcceptBlockOverrides(t *testing.T) {
	plan := validPlan()
	// Targets do not have to sum to one — a person typing 3 and 1 means 75/25.
	plan.Categories = []CategoryDef{{ID: "talk", Target: 3}, {ID: "music", Target: 1}}
	targets := plan.CategoryTargets(plan.Blocks[0], nil)
	if got := targets["talk"]; got < 0.749 || got > 0.751 {
		t.Fatalf("talk target = %.3f, want 0.75", got)
	}

	night := plan.Blocks[0]
	night.Balance = map[CategoryID]float64{"talk": 0.9, "music": 0.1}
	nightTargets := plan.CategoryTargets(night, nil)
	if got := nightTargets["talk"]; got < 0.89 || got > 0.91 {
		t.Fatalf("a block's own balance should win while it is on air, got %.3f", got)
	}
}

// ---- deriving a plan from an existing channel --------------------------

// A channel nobody has planned is not a special case: it gets the plan its
// sources and booked slots already describe, which is what let this rebuild
// land without a migration or a flag day.
func TestDerivePlanReproducesAnExistingChannel(t *testing.T) {
	channel := Channel{ID: "ch1", TalkShare: 0.8}
	sources := []Source{
		{ID: "pod1", Kind: SourcePodcastSubscription, Label: "Talk", Enabled: true, Role: RoleTalk},
		{ID: "mus1", Kind: SourceMusicPlaylist, Label: "Music", Enabled: true, Role: RoleMusic},
		{ID: "spots", Kind: SourceFilePool, Label: "Spots", Enabled: true, Role: RoleCommercial},
		{ID: "show1", Kind: SourcePodcastSubscription, Label: "The Show", Enabled: true, Role: RoleShow},
	}
	rules := []ScheduleRule{{
		ID: "r1", SourceID: "show1", Label: "The Show", WeekdayMask: 62,
		StartMinute: 16 * 60, EndMinute: 17 * 60, Enabled: true,
	}}

	plan := DerivePlan(channel, sources, rules, DefaultTalkShare)
	if err := plan.Validate(); err != nil {
		t.Fatalf("a derived plan must always be valid: %v", err)
	}
	if got := plan.Categories[0].Target; got != 0.8 {
		t.Fatalf("the channel's talk share should carry over, got %.2f", got)
	}

	general, ok := plan.Block("general")
	if !ok || !general.Default {
		t.Fatalf("the derived plan needs a default rotation block")
	}
	// The ninety-minute governor and the twenty-minute music set were constants
	// in the engine. They survive as what they always were: this station
	// owner's taste, written down where it can be changed.
	if len(general.Limits.MaxUnbroken) != 1 || general.Limits.MaxUnbroken[0].Max != "90m" {
		t.Fatalf("the derived plan should carry the talk-run limit, got %+v", general.Limits)
	}
	if len(general.Limits.MinUnbroken) != 1 || general.Limits.MinUnbroken[0].Min != "20m" {
		t.Fatalf("the derived plan should carry the music-set minimum, got %+v", general.Limits)
	}

	slot, ok := plan.Block("slot-r1")
	if !ok {
		t.Fatalf("the booked rule should become an anchored block")
	}
	if slot.Enter.At != "16:00" || slot.Exit.At != "17:00" || !slot.Enter.Hard {
		t.Fatalf("slot block has the wrong window: %+v", slot.Enter)
	}
	if slot.Enter.Days != "mon,tue,wed,thu,fri" {
		t.Fatalf("weekday mask 62 should be weekdays, got %q", slot.Enter.Days)
	}
	if slot.Enter.Start != StartImmediately {
		t.Fatalf("a derived slot keeps the old cut-in behaviour so nothing changes silently")
	}

	// Separator inventory gets a pool of its own and is NOT in the rotation.
	for _, ref := range general.Pools {
		pool, _ := plan.Pool(ref.Pool)
		for _, id := range pool.SourceIDs {
			if id == "spots" {
				t.Fatalf("a commercial pool must not be in the rotation — a stopset is not programming")
			}
			if id == "show1" {
				t.Fatalf("a show only airs in its slot, not from the rotation")
			}
		}
	}
}

func TestDerivePlanIsStableForTheSameChannel(t *testing.T) {
	channel := Channel{ID: "ch1"}
	sources := []Source{{ID: "pod1", Kind: SourcePodcastSubscription, Enabled: true, Role: RoleTalk}}
	rules := []ScheduleRule{
		{ID: "b", SourceID: "pod1", WeekdayMask: 127, StartMinute: 600, EndMinute: 660, Enabled: true},
		{ID: "a", SourceID: "pod1", WeekdayMask: 127, StartMinute: 300, EndMinute: 360, Enabled: true},
	}
	first := DerivePlan(channel, sources, rules, DefaultTalkShare)
	second := DerivePlan(channel, sources, rules, DefaultTalkShare)
	if len(first.Blocks) != len(second.Blocks) {
		t.Fatalf("derivation is not stable")
	}
	for index := range first.Blocks {
		if first.Blocks[index].ID != second.Blocks[index].ID {
			t.Fatalf("block order differs at %d: %q vs %q", index, first.Blocks[index].ID, second.Blocks[index].ID)
		}
	}
	// Sorted by start time, so a derived plan reads like the day does.
	if first.Blocks[1].ID != "slot-a" {
		t.Fatalf("slots should be ordered by start time, got %q first", first.Blocks[1].ID)
	}
}

// ---- small parsers -----------------------------------------------------

func TestParseDurationAcceptsBareMinutes(t *testing.T) {
	cases := map[string]time.Duration{
		"90":    90 * time.Minute,
		"90m":   90 * time.Minute,
		"1h30m": 90 * time.Minute,
		"":      0,
		"45s":   45 * time.Second,
	}
	for raw, want := range cases {
		got, err := parseDuration(raw)
		if err != nil {
			t.Fatalf("parseDuration(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("parseDuration(%q) = %s, want %s", raw, got, want)
		}
	}
	if _, err := parseDuration("soonish"); err == nil {
		t.Fatalf("expected an error for a non-duration")
	}
}

func TestParseWeekdays(t *testing.T) {
	cases := map[string]int{
		"*":           127,
		"":            127,
		"mon-fri":     62,
		"sat,sun":     65,
		"mon,wed,fri": 2 | 8 | 32,
		"fri-mon":     32 | 64 | 1 | 2, // wraps the week
	}
	for raw, want := range cases {
		got, err := parseWeekdays(raw)
		if err != nil {
			t.Fatalf("parseWeekdays(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("parseWeekdays(%q) = %d, want %d", raw, got, want)
		}
	}
	if _, err := parseWeekdays("funday"); err == nil {
		t.Fatalf("expected an error for a non-weekday")
	}
}

// ---- conditions --------------------------------------------------------

func TestConditionVocabulary(t *testing.T) {
	always, err := ParseCondition("")
	if err != nil || !always.Eval(ConditionContext{}) {
		t.Fatalf("an absent condition is vacuously true, got %v / %v", err, err)
	}

	window, err := ParseCondition("window >= 45m")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !window.Eval(ConditionContext{Window: time.Hour}) {
		t.Fatalf("an hour satisfies 'window >= 45m'")
	}
	if window.Eval(ConditionContext{Window: 30 * time.Minute}) {
		t.Fatalf("thirty minutes does not satisfy 'window >= 45m'")
	}
	// Nothing booked ahead means unbounded room, which satisfies every lower
	// bound and no upper one.
	if !window.Eval(ConditionContext{Window: 0}) {
		t.Fatalf("an unbounded window satisfies a lower bound")
	}
	upper, _ := ParseCondition("window < 20m")
	if upper.Eval(ConditionContext{Window: 0}) {
		t.Fatalf("an unbounded window must not satisfy an upper bound")
	}

	pool, err := ParseCondition("pool.fresh.available")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !pool.Eval(ConditionContext{PoolAvailable: func(id string) bool { return id == "fresh" }}) {
		t.Fatalf("pool condition should read the callback")
	}

	both, err := ParseCondition("window >= 45m && !pool.fresh.available")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !both.Eval(ConditionContext{Window: time.Hour, PoolAvailable: func(string) bool { return false }}) {
		t.Fatalf("conjunction with negation should hold")
	}

	if _, err := ParseCondition("moon is full"); err == nil {
		t.Fatalf("a plan that asks something the engine cannot answer must be rejected at save time")
	}
}
