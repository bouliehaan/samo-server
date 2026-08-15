package channels

import (
	"fmt"
	"time"
)

// This is the part of the scheduler that answers the first question:
//
//	WHAT KIND OF PROGRAMMING SHOULD BE HAPPENING RIGHT NOW?
//
// It never looks at an item. That separation is the whole point — the old
// engine went straight to "which file", so the only way to say anything about
// the shape of a day was to add another clause to the file-picking code, and
// the shape of the day ended up being six passes of Go nobody could change.

// ProgramState is the small amount of continuity a station carries between
// decisions.
//
// Persisted, because a restart in the middle of a sequence should resume the
// sequence rather than start the morning again — and because "how long have we
// been in this block" is not something the play log can answer once a block has
// no items of its own yet.
type ProgramState struct {
	BlockID   string    `json:"blockId"`
	EnteredAt time.Time `json:"enteredAt"`
	// ItemCount is how many items this block has aired, for count-limited
	// blocks and for the sequence patterns that arrive with obligations.
	ItemCount int `json:"itemCount"`
	// PatternIndex is where in a block's repeating cycle the station is.
	PatternIndex int `json:"patternIndex,omitempty"`
	// LastWasBreak marks the previous item as part of a separator.
	//
	// Needed because a break's own content is not programming, and without
	// knowing that, the rule "put a break between these two things" fires again
	// the moment the break ends — the break's last item and whatever follows it
	// are two things that want separating, so the station separates them, for
	// ever. No threshold fixes that; knowing what a break IS does.
	LastWasBreak bool `json:"lastWasBreak,omitempty"`
	// EnteredToday counts entries per block for the listening day named by
	// EnteredDay, so a block capped with `maxPerDay` knows how much of its
	// allowance is left. Keyed on the listening day rather than the calendar
	// one because that is the day the station's owner actually experiences.
	EnteredToday map[string]int `json:"enteredToday,omitempty"`
	EnteredDay   string         `json:"enteredDay,omitempty"`
	// Queue is programming already decided and not yet played — a break,
	// assembled as a unit so its items go out in order.
	//
	// Stored as references rather than resolved items on purpose: it is
	// re-validated against the world on the way out, so a queued spot whose
	// file has vanished, or which no longer fits before an appointment, is
	// dropped instead of played.
	Queue []QueuedItem `json:"queue,omitempty"`
}

// QueuedItem is one thing already decided.
type QueuedItem struct {
	SourceID string `json:"sourceId"`
	Ref      string `json:"ref"`
	// Reason is why it was queued, for the decision record.
	Reason string `json:"reason,omitempty"`
	// Position and Of describe where in a break this sits, so now-playing can
	// say "2 of 3" instead of leaving a listener wondering how long this goes on.
	Position int `json:"position,omitempty"`
	Of       int `json:"of,omitempty"`
}

// WantAt is what a block's pattern calls for at its current position.
//
// A block with no pattern always wants ordinary programming. A pattern that has
// run off its end wraps, because a cycle is a cycle.
func (b Block) WantAt(index int) WantKind {
	if len(b.Pattern) == 0 {
		return WantFill
	}
	step := b.Pattern[((index%len(b.Pattern))+len(b.Pattern))%len(b.Pattern)]
	if step.Want == "" {
		return WantFill
	}
	return step.Want
}

// BlockDecision is which block is on air and why.
type BlockDecision struct {
	Block       Block
	EnteredAt   time.Time
	EntryReason string
	// ExitReason describes what will end this block, in words, so the decision
	// record can say what the station is waiting for.
	ExitReason string
	// Anchor is the appointment that put this block on air, if one did.
	Anchor *Anchor
	State  ProgramState
	// CutAtBoundary asks this pass to pick something that will be cut when the
	// next appointment starts. Set only by the gap-filling retry; see
	// ProgrammingIntent.CutAtBoundary for why the fit rule stands down for it.
	CutAtBoundary bool
	// Changed reports whether this decision moved the station to a new block.
	Changed bool
}

// ResolveBlock decides which block the station is in.
//
// Precedence, and the reasoning for it:
//
//  1. A hard anchor covering now. An appointment is the one thing that is not
//     negotiable; it is why anybody books one.
//  2. The block we were already in, if nothing has ended it. Stickiness is what
//     makes a block a block rather than a re-derivation at every item.
//  3. A time-entered block whose window covers now. Dayparts without the
//     bluntness of an appointment.
//  4. Whatever the ended block hands over to, following `next` until something
//     accepts.
//  5. The default block, which always accepts.
func ResolveBlock(plan Plan, timeline Timeline, state ProgramState, cond ConditionContext, now time.Time) BlockDecision {
	// 1 — an appointment.
	if timeline.Active != nil {
		if block, ok := plan.Block(timeline.Active.BlockID); ok {
			anchor := *timeline.Active
			decision := BlockDecision{
				Block:  block,
				Anchor: &anchor,
				EntryReason: fmt.Sprintf("booked slot %q is on air (%s–%s)",
					anchor.Label, anchor.Start.Format("15:04"), anchor.End.Format("15:04")),
				ExitReason: fmt.Sprintf("runs until %s", anchor.End.Format("15:04")),
			}
			return withState(decision, state, block, now)
		}
	}

	// 2 — stay where we are, unless something ended it.
	if current, ok := plan.Block(state.BlockID); ok && !state.EnteredAt.IsZero() {
		// The default block is the fallback, not a trap. It has no exit
		// condition of its own — that is what makes it the fallback — so
		// without this a station that lands in it stays there for ever and
		// every daypart after the first is unreachable. Anything with an
		// explicit entry outranks it the moment that entry is satisfied.
		if current.Default {
			if scheduled, found := scheduledBlockFor(plan, timeline, cond, now); found && scheduled.ID != current.ID {
				decision := BlockDecision{
					Block:       scheduled,
					EntryReason: "its window opened, and the default block yields to anything with a claim",
					ExitReason:  blockExitDescription(scheduled),
				}
				return withState(decision, state, scheduled, now)
			}
		}
		// An anchored block whose window has closed is over by definition; the
		// active-anchor branch above would have caught it otherwise.
		anchored := current.Enter.Hard && current.Enter.At != ""
		reason, ended := blockExitFired(plan, current, state, timeline, cond, now)
		if !ended && !anchored {
			return BlockDecision{
				Block:       current,
				EnteredAt:   state.EnteredAt,
				EntryReason: "continuing",
				ExitReason:  blockExitDescription(current),
				State:       state,
			}
		}
		if !ended && anchored {
			reason = "its booked window has ended"
		}
		// 4 — hand over.
		next := followNext(plan, current, cond, now)
		decision := BlockDecision{
			Block:       next,
			EntryReason: fmt.Sprintf("%s after %q (%s)", handoverVerb(current, next), blockName(current), reason),
			ExitReason:  blockExitDescription(next),
		}
		return withState(decision, state, next, now)
	}

	// 3 — a daypart whose window covers now.
	if block, ok := scheduledBlockFor(plan, timeline, cond, now); ok {
		decision := BlockDecision{
			Block:       block,
			EntryReason: fmt.Sprintf("scheduled from %s", block.Enter.At),
			ExitReason:  blockExitDescription(block),
		}
		return withState(decision, state, block, now)
	}

	// 5 — the default.
	block := plan.DefaultBlock()
	decision := BlockDecision{
		Block:       block,
		EntryReason: "nothing else claims this hour",
		ExitReason:  blockExitDescription(block),
	}
	return withState(decision, state, block, now)
}

func withState(decision BlockDecision, previous ProgramState, block Block, now time.Time) BlockDecision {
	if previous.BlockID == block.ID && !previous.EnteredAt.IsZero() {
		decision.EnteredAt = previous.EnteredAt
		decision.State = previous
		if decision.EntryReason == "" {
			decision.EntryReason = "continuing"
		}
		return decision
	}
	decision.EnteredAt = now
	decision.Changed = true
	decision.State = enteringBlock(previous, block.ID, now)
	return decision
}

// countEntry records that a block has just been entered, for the daily caps.
//
// Only capped blocks are counted. An uncapped one would grow the state document
// by a key per block per day for a number nothing ever reads.
func countEntry(previous ProgramState, blockID string) map[string]int {
	out := map[string]int{}
	for id, count := range previous.EnteredToday {
		out[id] = count
	}
	out[blockID]++
	return out
}

// rollListeningDay clears the per-day entry counts when the listening day named
// in the state is no longer the one the station is in.
//
// Keyed on a day STRING rather than a timestamp comparison because the listening
// day is wall-clock in the channel's zone, and the state is stored in UTC — the
// same mismatch that made the old schedule rules fire an hour out.
func rollListeningDay(state ProgramState, day ListeningDay, loc *time.Location, now time.Time) ProgramState {
	today := listeningDayKey(day, loc, now)
	if state.EnteredDay == today {
		return state
	}
	state.EnteredDay = today
	state.EnteredToday = nil
	return state
}

// listeningDayKey names the listening day `now` falls in. The small hours
// before the day opens still belong to the day that just ended, so a block
// capped at once a day cannot come round again at 02:00.
func listeningDayKey(day ListeningDay, loc *time.Location, now time.Time) string {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	if minutes := local.Hour()*60 + local.Minute(); minutes < day.normalized().StartMinute {
		local = local.AddDate(0, 0, -1)
	}
	return local.Format("2006-01-02")
}

func handoverVerb(from, to Block) string {
	if from.Next == to.ID {
		return "handed over to"
	}
	return "fell back to"
}

func blockName(block Block) string { return firstNonEmpty(block.Label, block.ID) }

// followNext walks the handover chain until a block accepts.
//
// A block that declines — its `when` is false, its pools are empty — passes the
// hour on rather than leaving the station with nothing, and the walk terminates
// at the default block, which always accepts. The step limit is belt and
// braces: plan validation rejects cycles, but a walk that can run forever
// inside a live streamer is not a risk worth taking on a validator.
func followNext(plan Plan, from Block, cond ConditionContext, now time.Time) Block {
	current := from
	for steps := 0; steps <= len(plan.Blocks); steps++ {
		if current.Next == "" {
			break
		}
		next, ok := plan.Block(current.Next)
		if !ok {
			break
		}
		if blockAccepts(next, cond, now) {
			return next
		}
		current = next
	}
	return plan.DefaultBlock()
}

// blockAccepts reports whether a block is willing to take over right now.
func blockAccepts(block Block, cond ConditionContext, now time.Time) bool {
	if len(block.Pools) == 0 && !block.Default {
		return false
	}
	// Its allowance for the day, before anything else is asked. A block that
	// has had its turn is not a candidate, however true its condition is.
	if block.Enter.MaxPerDay > 0 && cond.EnteredToday[block.ID] >= block.Enter.MaxPerDay {
		return false
	}
	condition, err := ParseCondition(block.Enter.When)
	if err != nil {
		return false
	}
	if !condition.Eval(cond) {
		return false
	}
	// A block that also names a time only accepts inside its own window, so
	// "after the news" plus "07:00–08:00" means both, not either.
	if block.Enter.At != "" {
		if !blockWindowContains(block, now) {
			return false
		}
	}
	return true
}

// scheduledBlockFor finds a time-entered block whose window covers now.
// The latest-starting match wins, so a specific block layered over a broader
// one behaves the way a person reading the schedule would expect.
func scheduledBlockFor(plan Plan, _ Timeline, cond ConditionContext, now time.Time) (Block, bool) {
	var best Block
	bestStart := -1
	// A block with no clock time but a condition is a MODE rather than a
	// daypart: "while the station still owes you episodes", "while there is
	// nothing booked for hours". Without this it could never be entered at all
	// — the loop below is keyed on Enter.At — so a plan could express the
	// condition, save it, display it, and never once act on it.
	//
	// A timed block still wins: an appointment is a promise about the clock,
	// and a mode is only what the station does the rest of the time.
	var conditional Block
	haveConditional := false
	for _, block := range plan.Blocks {
		if block.Default {
			continue
		}
		if block.Enter.At == "" {
			if block.Enter.When == "" || haveConditional {
				continue
			}
			if !blockAccepts(block, cond, now) {
				continue
			}
			conditional, haveConditional = block, true
			continue
		}
		if !blockWindowContains(block, now) {
			continue
		}
		if !blockAccepts(block, cond, now) {
			continue
		}
		start, err := parseClock(block.Enter.At)
		if err != nil {
			continue
		}
		if start > bestStart {
			best, bestStart = block, start
		}
	}
	if bestStart >= 0 {
		return best, true
	}
	return conditional, haveConditional
}

// blockWindowContains reports whether a time-entered block's own window covers
// a moment, in the block's declared days.
func blockWindowContains(block Block, now time.Time) bool {
	start, err := parseClock(block.Enter.At)
	if err != nil {
		return false
	}
	mask, err := parseWeekdays(block.Enter.Days)
	if err != nil {
		mask = 127
	}
	loc := now.Location()
	day := startOfDay(now, loc)
	startAt := wallClock(day, start, loc)
	if now.Before(startAt) {
		// Might still be inside yesterday's occurrence of a window that crosses
		// midnight.
		day = day.AddDate(0, 0, -1)
		startAt = wallClock(day, start, loc)
	}
	if mask&(1<<int(startAt.Weekday())) == 0 {
		return false
	}
	end := anchorEnd(block, startAt, loc)
	if end.IsZero() {
		// No stated end: the window is the rest of that day.
		end = wallClock(startOfDay(startAt, loc).AddDate(0, 0, 1), 0, loc)
	}
	return !now.Before(startAt) && now.Before(end)
}

// blockExitFired reports whether anything has ended the current block, and what.
func blockExitFired(plan Plan, block Block, state ProgramState, timeline Timeline, cond ConditionContext, now time.Time) (string, bool) {
	exit := block.Exit
	if exit.Count > 0 && state.ItemCount >= exit.Count {
		return fmt.Sprintf("played its %d items", exit.Count), true
	}
	if exit.Duration != "" {
		if duration, err := parseDuration(exit.Duration); err == nil && duration > 0 {
			if !now.Before(state.EnteredAt.Add(duration)) {
				return fmt.Sprintf("ran its %s", duration), true
			}
		}
	}
	if exit.At != "" {
		if minute, err := parseClock(exit.At); err == nil {
			loc := now.Location()
			end := wallClock(startOfDay(state.EnteredAt.In(loc), loc), minute, loc)
			if !end.After(state.EnteredAt) {
				end = wallClock(startOfDay(state.EnteredAt.In(loc), loc).AddDate(0, 0, 1), minute, loc)
			}
			if !now.Before(end) {
				return "reached " + exit.At, true
			}
		}
	}
	if exit.AtNextAnchor && timeline.Next != nil && !now.Before(timeline.Next.Start) {
		return "the next booked slot is due", true
	}
	if exit.When != "" {
		if condition, err := ParseCondition(exit.When); err == nil && condition.Eval(cond) {
			return exit.When, true
		}
	}
	// A block whose pools have all gone empty has nothing to say. Handing over
	// beats going quiet, and it is how "run the fresh queue until it is empty"
	// terminates without anybody writing an exit condition for it.
	if len(block.Pools) > 0 && cond.PoolAvailable != nil && !block.Default {
		available := false
		for _, ref := range block.Pools {
			if cond.PoolAvailable(ref.Pool) {
				available = true
				break
			}
		}
		if !available {
			return "its pools have nothing left to play", true
		}
	}
	_ = plan
	return "", false
}

// blockExitDescription says, in words, what the station is waiting for.
func blockExitDescription(block Block) string {
	exit := block.Exit
	switch {
	case exit.Count > 0:
		return fmt.Sprintf("after %d items", exit.Count)
	case exit.Duration != "":
		return "after " + exit.Duration
	case exit.At != "":
		return "at " + exit.At
	case exit.AtNextAnchor:
		return "when the next booked slot is due"
	case exit.When != "":
		return "when " + exit.When
	case block.Default:
		return "runs until something else claims the hour"
	default:
		return "when its pools run dry"
	}
}

// ---- the intent --------------------------------------------------------

// ProgrammingIntent is the answer to "what kind of programming should be
// happening right now", handed to the part that picks an actual item.
type ProgrammingIntent struct {
	Block       Block
	BlockLabel  string
	EnteredAt   time.Time
	EntryReason string
	ExitReason  string
	// Window is the hard fit: an item must finish inside it. Zero means
	// unbounded — nothing is booked ahead.
	//
	// Deliberately NOT set while a booked block is on air. Inside its own slot
	// the point is to fill the slot: a sixty-minute show in a sixty-minute
	// window that we joined five minutes late should play and be cut at the
	// boundary, not be ruled out for not fitting. Filling the slot is the whole
	// reason for booking one.
	Window time.Duration
	// PlayCeiling is how long the station may STAY on whatever it picks. It is
	// the answer for content with no length of its own — a live stream never
	// ends, so nothing downstream would ever move off it.
	PlayCeiling time.Duration
	// TargetDuration is what this block would LIKE the next item to be, when it
	// has an opinion — a twelve-minute music transition wants something that
	// fits twelve minutes. Zero means no preference.
	TargetDuration time.Duration
	// Targets is each category's share of airtime while this block is on.
	Targets map[CategoryID]float64
	Pools   []PoolRef
	Limits  []ResolvedLimit

	// CutAtBoundary means this pick exists to hold the last of a block's time
	// and is ALLOWED to be cut when the appointment starts.
	//
	// The fit rule normally refuses anything that would overrun an
	// appointment, which is right for programming and wrong for the forty
	// seconds in front of one: nothing the station owns is forty seconds long,
	// so the fit rule empties the candidate set and the appointment is dragged
	// forward to meet the silence. A booked show that begins whenever the last
	// track happened to end is not booked. So for this pass — and only this
	// pass, over a pool the plan has nominated as cuttable — the rule stands
	// down, the item is capped at the gap and faded out on the boundary.
	CutAtBoundary bool

	// Want is what this position in the block's cycle calls for.
	Want WantKind
	// Exposure is how much airing something here counts toward satisfying an
	// obligation, 0..1.
	Exposure float64
	// MaxUrgency is the most urgent thing the station owes right now, so
	// freshness can be scored relative to today rather than to an absolute.
	MaxUrgency float64
}

// ResolvedLimit is a block limit with its durations parsed and its current run
// measured, so the decision record can show how close to it the station is.
type ResolvedLimit struct {
	Category   CategoryID
	Max        time.Duration
	ResetAfter time.Duration
	MinItem    time.Duration
	Run        time.Duration
}

// Remaining is how much of this category may still run before the limit bites.
//
// It reaches zero, and that is the whole point. Flooring it at MinItem — so a
// station deep into a run could always still air something short — meant the
// limit could never actually stop anything: there was permanently room for one
// more twenty-minute item, and then one more, and a ninety-minute ceiling
// produced a three-hour run of talk. A ceiling that cannot be reached is not a
// ceiling.
//
// The worry the floor was answering is real but belongs somewhere else: if
// nothing at all fits, the relaxation ladder gives this rule up (it is the last
// one to go) and the station plays something short rather than falling silent.
// That way the fallback happens when the station has genuinely run out of
// options, not on every single decision.
func (l ResolvedLimit) Remaining() time.Duration {
	if remaining := l.Max - l.Run; remaining > 0 {
		return remaining
	}
	return 0
}

// Exceeded reports whether the run has passed its limit.
func (l ResolvedLimit) Exceeded() bool { return l.Run >= l.Max }

// resolveLimits parses a block's limits and measures each one's current run
// WITHIN this block.
func resolveLimits(block Block, tail []PlayTailEntry, enteredAt time.Time) []ResolvedLimit {
	out := make([]ResolvedLimit, 0, len(block.Limits.MaxUnbroken))
	for _, limit := range block.Limits.MaxUnbroken {
		max, err := parseDuration(limit.Max)
		if err != nil || max <= 0 {
			continue
		}
		resetAfter, _ := parseDuration(limit.ResetAfter)
		minItem, _ := parseDuration(limit.MinItem)
		out = append(out, ResolvedLimit{
			Category:   limit.Category,
			Max:        max,
			ResetAfter: resetAfter,
			MinItem:    minItem,
			Run:        CategoryRun(tail, limit.Category, resetAfter, enteredAt),
		})
	}
	return out
}

// enteringBlock is the state for a block the station is moving into.
//
// The daily allowances have to survive the move. Building a fresh ProgramState
// at every entry point drops them, and a cap that resets whenever the station
// changes block is not a cap — it is the same defect as a break policy that only
// chained in production, and it hides in exactly the same way, because in-memory
// the counts look right until something re-enters by a different route.
func enteringBlock(previous ProgramState, blockID string, now time.Time) ProgramState {
	return ProgramState{
		BlockID:      blockID,
		EnteredAt:    now,
		EnteredDay:   previous.EnteredDay,
		EnteredToday: countEntry(previous, blockID),
	}
}
