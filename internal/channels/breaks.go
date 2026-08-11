package channels

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// A break is a unit of programming, not one item on a clock.
//
// The old version played one spot every twenty minutes, which is not a stopset
// — it is a metronome. And "play two songs" is not a specification either: two
// fifteen-minute songs is a half-hour break, two thirty-second songs is a
// minute, and a station that only counts items will produce both. So a break
// policy states BOTH a count range and a duration range, and the planner finds
// the combination that satisfies them.
//
// Nothing in here requires commercials to exist. An element whose pool is empty
// contributes nothing and the elastic element takes up the slack — degradation
// is the ordinary path through the same code, not a branch.

// breakShelf is one element of a break policy together with what its pool can
// currently offer, best first.
type breakShelf struct {
	element    BreakElement
	candidates []ScoredCandidate
}

// PlannedBreak is an assembled separator, ready to play in order.
type PlannedBreak struct {
	Items []Candidate
	// Duration is what it will actually run to.
	Duration time.Duration
	// Miss is how far off the target it landed. Reported rather than hidden:
	// a break that is consistently four minutes short is a library problem, and
	// the station should be able to say so.
	Miss time.Duration
	// InRange is whether it satisfied the policy's accept range.
	InRange bool
	// Note explains the compromise, when there was one.
	Note string
}

// Empty reports whether the planner found nothing at all to play.
func (b PlannedBreak) Empty() bool { return len(b.Items) == 0 }

// breakDue reports whether a break should be inserted before the next item.
//
// Asked AFTER the station knows what it would play next, because a break
// between two music tracks is not a break, it is an interruption — the point of
// a stopset is to separate the things worth separating.
func breakDue(policy *BreakPolicy, state ProgramState, tail []PlayTailEntry, next CategoryID, now time.Time, interstitials map[string]bool) (bool, string) {
	if policy == nil || len(policy.Elements) == 0 {
		return false, ""
	}
	if !policy.separates(next) {
		return false, ""
	}
	// Nothing has aired yet: a station does not open with a stopset.
	if len(tail) == 0 {
		return false, ""
	}
	// A break does not follow a break. Its own content is not the programming
	// being separated, and without this the rule re-fires on the break's own
	// last item and never stops.
	if state.LastWasBreak || len(state.Queue) > 0 {
		return false, ""
	}
	previous := tail[0]
	if !policy.separates(previous.Category) {
		return false, ""
	}
	if gap := policy.minGap(); gap > 0 {
		if since, ok := timeSinceLastBreak(tail, now, interstitials); ok && since < gap {
			return false, ""
		}
	}
	return true, fmt.Sprintf("separating %s from %s", previous.Category, next)
}

// timeSinceLastBreak finds how long ago the station last played a separator.
func timeSinceLastBreak(tail []PlayTailEntry, now time.Time, interstitials map[string]bool) (time.Duration, bool) {
	for _, entry := range tail {
		if interstitials[entry.SourceID] {
			return now.Sub(entry.StartedAt), true
		}
	}
	return 0, false
}

// planBreak assembles the best break it can from the policy's elements.
//
// The search is deliberately exhaustive over COUNTS rather than over items:
// element counts are small (nought to three), so every combination can be
// tried, and within a combination the best-scoring items are taken in order.
// That gives the planner a real choice about shape — one long song or two short
// ones — which is the whole point of stating a duration.
func (e *Engine) planBreak(
	ctx context.Context,
	policy *BreakPolicy,
	env enumerationContext,
	scoring scoreEnv,
	constraints constraintEnv,
	window time.Duration,
) PlannedBreak {
	if policy == nil {
		return PlannedBreak{}
	}

	// What each element can actually offer, best first.
	shelves := make([]breakShelf, 0, len(policy.Elements))
	for _, element := range policy.Elements {
		available := e.poolCandidates(ctx, element.Pool, env)
		survivors, _, _ := applyConstraints(available, constraints)
		if len(survivors) == 0 {
			// An empty shelf is not an error. This is the whole "no
			// commercials configured" case, and it needs no branch.
			shelves = append(shelves, breakShelf{element: element})
			continue
		}
		shelves = append(shelves, breakShelf{element: element, candidates: scoreCandidates(survivors, scoring)})
	}

	target := policy.targetDuration()
	minItems, maxItems := policy.Accept.itemRange(policy.Target.Items)
	minDuration, maxDuration := policy.Accept.durationRange()

	best := PlannedBreak{Miss: -1}
	counts := make([]int, len(shelves))
	var walk func(index int)
	walk = func(index int) {
		if index == len(shelves) {
			plan := assembleBreak(shelves, counts, policy)
			if len(plan.Items) == 0 {
				return
			}
			total := 0
			for _, count := range counts {
				total += count
			}
			if total < minItems || (maxItems > 0 && total > maxItems) {
				return
			}
			if window > 0 && plan.Duration > window {
				return
			}
			plan.InRange = withinDuration(plan.Duration, minDuration, maxDuration)
			plan.Miss = missFrom(plan.Duration, target)
			if best.Miss < 0 || betterBreak(plan, best) {
				best = plan
			}
			return
		}
		low, high := shelves[index].element.countRange()
		if available := len(shelves[index].candidates); high > available {
			high = available
		}
		if low > high {
			low = 0
		}
		for count := low; count <= high; count++ {
			counts[index] = count
			walk(index + 1)
		}
		counts[index] = 0
	}
	walk(0)

	if best.Miss < 0 {
		return PlannedBreak{}
	}
	if !best.InRange {
		best.Note = fmt.Sprintf("closest the library allows: %s against a %s target",
			round(best.Duration), round(target))
	}
	return best
}

// assembleBreak builds one candidate break from a set of per-element counts.
//
// Items are never truncated to hit a duration. That is stated here because it
// is the one thing a duration-driven planner is tempted to do, and cutting a
// song off at 2:40 because the arithmetic wanted 2:40 is not radio.
func assembleBreak(shelves []breakShelf, counts []int, policy *BreakPolicy) PlannedBreak {
	first := []Candidate{}
	middle := []Candidate{}
	last := []Candidate{}
	total := time.Duration(0)
	seen := map[string]bool{}
	creators := map[string]bool{}

	for index, shelf := range shelves {
		taken := 0
		for _, candidate := range shelf.candidates {
			if taken >= counts[index] {
				break
			}
			// The same file reachable through two elements is one item.
			if candidate.Candidate.Ref != "" {
				if seen[candidate.Candidate.Ref] {
					continue
				}
			}
			// A break separates itself too. The constraints ran against the
			// station's HISTORY, which has nothing to say about two items being
			// put next to each other inside a break that has not aired yet —
			// and two songs by the same artist, back to back, is the same
			// mistake wherever it happens.
			creator := candidate.Candidate.Creator
			if creator != "" && candidate.Candidate.Traits.HasCreator {
				if creators[creator] {
					continue
				}
				creators[creator] = true
			}
			if candidate.Candidate.Ref != "" {
				seen[candidate.Candidate.Ref] = true
			}
			length := candidate.Candidate.Duration
			if length <= 0 {
				length = unknownItemLength
			}
			total += length
			switch shelf.element.Position {
			case "first":
				first = append(first, candidate.Candidate)
			case "last":
				last = append(last, candidate.Candidate)
			default:
				middle = append(middle, candidate.Candidate)
			}
			taken++
		}
	}
	_ = policy
	items := append(append(append([]Candidate{}, first...), middle...), last...)
	return PlannedBreak{Items: items, Duration: total}
}

func withinDuration(actual, low, high time.Duration) bool {
	if low > 0 && actual < low {
		return false
	}
	if high > 0 && actual > high {
		return false
	}
	return true
}

func missFrom(actual, target time.Duration) time.Duration {
	if target <= 0 {
		return 0
	}
	if actual > target {
		return actual - target
	}
	return target - actual
}

// betterBreak prefers one that satisfies the accept range, then one closer to
// the target.
func betterBreak(candidate, best PlannedBreak) bool {
	if candidate.InRange != best.InRange {
		return candidate.InRange
	}
	if candidate.Miss != best.Miss {
		return candidate.Miss < best.Miss
	}
	// A tie on shape goes to the shorter break: a separator that runs long is
	// more annoying than one that runs short.
	return candidate.Duration < best.Duration
}

// poolCandidates enumerates one pool, for the break planner.
func (e *Engine) poolCandidates(ctx context.Context, poolID string, env enumerationContext) []Candidate {
	pool, ok := e.Plan.Pool(poolID)
	if !ok {
		return nil
	}
	out := []Candidate{}
	seen := map[string]bool{}
	for _, src := range pool.Resolve(e.Sources) {
		if !src.Enabled {
			continue
		}
		for _, candidate := range e.enumerateSource(ctx, src, env) {
			key := src.ID + "\x00" + candidate.Ref
			if candidate.Ref != "" {
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			candidate.PoolID = poolID
			candidate.PoolWeight = 1
			out = append(out, candidate)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// BreakSummary is a planned break as the decision record sees it.
type BreakSummary struct {
	Items    []string `json:"items"`
	Minutes  int      `json:"minutes"`
	TargetM  int      `json:"targetMinutes,omitempty"`
	InRange  bool     `json:"inRange"`
	Reason   string   `json:"reason,omitempty"`
	Note     string   `json:"note,omitempty"`
	Position int      `json:"position,omitempty"`
	Of       int      `json:"of,omitempty"`
}
