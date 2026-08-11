package channels

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"
)

// The simulator runs the real scheduler forward against a virtual clock and an
// in-memory play log.
//
// It is the single most useful thing in this package. Every rule the engine has
// is a rule about time — balance over a window, separation, repeats, what is
// booked next — so before this existed the only way to find out whether a
// change produced good radio was to put it on the air and listen for nine
// hours. Now three days of programming can be inspected in under a second, with
// every decision's reasoning attached, and a bad idea is caught in a test
// instead of at four in the morning.
//
// Nothing here is a second implementation of the scheduler. It calls
// Engine.Decide exactly as the streamer does; the only differences are where
// the clock comes from and where the play log goes.

// SimOptions configures a run.
type SimOptions struct {
	Start    time.Time
	Duration time.Duration
	// MaxSteps stops a pathological plan (one that only ever picks
	// four-second items) from running for a million iterations.
	MaxSteps int
	// Seed overrides the plan's own seed, so the same station can be run
	// several times to see how much its choices actually vary.
	Seed int64
	// Warmup is history to fabricate before the run starts, so a simulation can
	// begin from "the station has been playing talk all night" rather than from
	// a station that has never played anything.
	Warmup []MemoryPlay
}

// SimStep is one item the simulated station played.
type SimStep struct {
	At       time.Time     `json:"at"`
	Ends     time.Time     `json:"ends"`
	Length   time.Duration `json:"-"`
	Item     PlaybackItem  `json:"item"`
	Decision Decision      `json:"decision"`
}

// SimGap is a moment the station could not decide anything.
type SimGap struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

// SimResult is everything a run produced.
type SimResult struct {
	Steps   []SimStep      `json:"steps"`
	Gaps    []SimGap       `json:"gaps"`
	Report  SimReport      `json:"report"`
	History *MemoryHistory `json:"-"`
}

// SimReport is the summary a person actually reads.
type SimReport struct {
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Items int       `json:"items"`

	Blocks     []SimBlockSpan  `json:"blocks"`
	Categories []SimCategory   `json:"categories"`
	Sources    []SimNamedTotal `json:"sources"`
	Creators   []SimNamedTotal `json:"creators"`
	Anchors    []SimAnchor     `json:"anchors"`

	// LongestRun is the longest unbroken stretch of each category, which is the
	// number that says whether the station is listenable. An hour of talk is
	// radio; nine is a hostage situation.
	LongestRun []SimNamedTotal `json:"longestRun"`
	// BackToBackCreator counts consecutive items by the same person. Should be
	// zero, and if it is not, the separation rules are being relaxed.
	BackToBackCreator int `json:"backToBackCreator"`
	// BackToBackWhen names the offenders. A count on its own tells you there is
	// a problem; this tells you where to look.
	BackToBackWhen []string `json:"backToBackWhen,omitempty"`
	// Relaxations is how often each constraint had to be given up.
	Relaxations map[string]int `json:"relaxations,omitempty"`
	// Interstitials is how many separator items aired.
	Interstitials int `json:"interstitials"`
	Gaps          int `json:"gaps"`

	// Obligations is how the run handled what the station owed.
	Obligations SimObligations `json:"obligations"`
	// Breaks is what the separators actually looked like.
	Breaks SimBreaks `json:"breaks"`
}

// SimObligations is the lifecycle of what the station owed across a run.
type SimObligations struct {
	Surfaced int `json:"surfaced"`
	// StillOwed is what was never got to. A number that climbs across a long
	// run means the station is generating obligations faster than it airs them,
	// which is a programming problem rather than a scheduling one.
	StillOwed int `json:"stillOwed"`
	// MedianWaitMinutes is how long something owed waited before it aired.
	MedianWaitMinutes int `json:"medianWaitMinutes"`
	// SlowestWaitMinutes is the worst case.
	SlowestWaitMinutes int `json:"slowestWaitMinutes"`
}

// SimBreaks is the shape of the separators a run produced.
type SimBreaks struct {
	Count int `json:"count"`
	// MeanMinutes and MeanItems are what a break actually came out as, against
	// what the policy asked for. A consistent miss is a library problem the
	// station should be able to see.
	MeanMinutes float64 `json:"meanMinutes"`
	MeanItems   float64 `json:"meanItems"`
	OutOfRange  int     `json:"outOfRange"`
}

// SimBlockSpan is one continuous stretch in one block.
type SimBlockSpan struct {
	BlockID string    `json:"blockId"`
	Label   string    `json:"label"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Items   int       `json:"items"`
}

// SimCategory is one category's airtime across the run.
type SimCategory struct {
	Category CategoryID `json:"category"`
	Minutes  int        `json:"minutes"`
	Percent  int        `json:"percent"`
}

// SimNamedTotal is a name and a number of minutes.
type SimNamedTotal struct {
	Name    string `json:"name"`
	Minutes int    `json:"minutes"`
}

// SimAnchor is a booked slot and whether the run actually honoured it.
type SimAnchor struct {
	BlockID   string    `json:"blockId"`
	Label     string    `json:"label"`
	Due       time.Time `json:"due"`
	StartedAt time.Time `json:"startedAt,omitempty"`
	LateBy    string    `json:"lateBy,omitempty"`
	// EarlyBy is set when the appointment was brought forward because nothing
	// the station owns fitted in the gap left in front of it.
	EarlyBy string `json:"earlyBy,omitempty"`
	Missed  bool   `json:"missed,omitempty"`
}

// unknownItemLength is what the simulator assumes for an item whose length
// nobody knows — a file that has never been probed.
//
// Only the simulation needs a number here; the real streamer finds out by
// playing it. Four minutes is a track, which is what most unprobed files in a
// pool turn out to be.
const unknownItemLength = 4 * time.Minute

// Simulate runs a station forward without broadcasting.
//
// The engine's History must be a *MemoryHistory: the whole point is that the
// run leaves no trace on the real station's play log, and passing the SQL
// history here would write a night of imaginary programming into it.
func Simulate(ctx context.Context, engine *Engine, opts SimOptions) (SimResult, error) {
	history, ok := engine.History.(*MemoryHistory)
	if !ok {
		return SimResult{}, fmt.Errorf("simulation needs an in-memory history, not %T", engine.History)
	}
	for _, warmup := range opts.Warmup {
		history.Record(warmup)
	}

	loc := engine.location()
	start := opts.Start.In(loc)
	duration := opts.Duration
	if duration <= 0 {
		duration = 24 * time.Hour
	}
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 20000
	}
	end := start.Add(duration)

	result := SimResult{History: history}
	state := ProgramState{}
	now := start
	consecutiveFailures := 0

	for step := 0; step < maxSteps && now.Before(end); step++ {
		seed := opts.Seed
		if seed == 0 {
			seed = decisionSeed(engine.Plan, engine.Channel.ID, now)
		} else {
			seed ^= now.Unix()
		}
		engine.Rand = rand.New(rand.NewSource(seed))

		item, decision, next, err := engine.Decide(ctx, now, state)
		if err != nil {
			result.Gaps = append(result.Gaps, SimGap{At: now, Reason: firstNonEmpty(decision.Error, err.Error())})
			consecutiveFailures++
			if consecutiveFailures > 8 {
				// The station has nothing to play and nothing is going to
				// change by asking again a minute later. Stop rather than fill
				// the report with thousands of identical failures.
				break
			}
			state = next
			now = now.Add(time.Minute)
			continue
		}
		consecutiveFailures = 0

		length := simItemLength(item)
		ends := now.Add(length)
		// Credit obligations the same way the streamer does, or a simulated day
		// would surface the same new episode forever.
		if engine.Obligations != nil && item.ItemRef != "" && item.Exposure > 0 {
			completed := item.DurationSeconds <= 0 ||
				length >= time.Duration(item.DurationSeconds)*time.Second
			credit := item.Exposure * playedFraction(item, length, completed)
			if credit > 0 {
				_ = engine.Obligations.Credit(ctx, item.ItemRef, credit, ends)
			}
		}
		history.Record(MemoryPlay{
			SourceID:        item.SourceID,
			ItemRef:         item.ItemRef,
			Artist:          item.Artist,
			Category:        item.Category,
			StartedAt:       now,
			EndedAt:         ends,
			DurationSeconds: int(length / time.Second),
		})
		result.Steps = append(result.Steps, SimStep{
			At: now, Ends: ends, Length: length, Item: item, Decision: decision,
		})
		state = next
		now = ends
	}

	result.Report = buildSimReport(engine, result, start, now)
	return result, nil
}

// simItemLength is how long the simulated station stays on an item.
func simItemLength(item PlaybackItem) time.Duration {
	if item.DurationSeconds > 0 {
		length := time.Duration(item.DurationSeconds) * time.Second
		if item.MaxDuration > 0 && item.MaxDuration < length {
			return item.MaxDuration
		}
		return length
	}
	if item.MaxDuration > 0 {
		return item.MaxDuration
	}
	return unknownItemLength
}

func buildSimReport(engine *Engine, result SimResult, from, to time.Time) SimReport {
	report := SimReport{
		From:        from,
		To:          to,
		Items:       len(result.Steps),
		Gaps:        len(result.Gaps),
		Relaxations: map[string]int{},
	}

	byCategory := map[CategoryID]time.Duration{}
	bySource := map[string]time.Duration{}
	byCreator := map[string]time.Duration{}
	longest := map[CategoryID]time.Duration{}
	var runCategory CategoryID
	var runLength time.Duration
	var lastCreator string
	total := time.Duration(0)

	for index, step := range result.Steps {
		byCategory[step.Item.Category] += step.Length
		total += step.Length
		label := step.Item.SourceLabel
		if label == "" {
			label = step.Item.SourceID
		}
		bySource[label] += step.Length

		creator := strings.TrimSpace(step.Item.Artist)
		if creator == "" {
			if src, ok := engine.source(step.Item.SourceID); ok && TraitsFor(src).HasCreator {
				creator = CreatorOf(src)
			}
		}
		if creator != "" {
			byCreator[creator] += step.Length
			if index > 0 && creator == lastCreator {
				report.BackToBackCreator++
				if len(report.BackToBackWhen) < 8 {
					report.BackToBackWhen = append(report.BackToBackWhen, fmt.Sprintf(
						"%s  %s: %q then %q (%s)",
						step.At.Format("Mon 15:04"), creator,
						truncate(result.Steps[index-1].Item.Title, 28),
						truncate(step.Item.Title, 28),
						firstNonEmpty(step.Decision.BlockLabel, step.Decision.BlockID)))
				}
			}
		}
		lastCreator = creator

		if src, ok := engine.source(step.Item.SourceID); ok && TraitsFor(src).Interstitial {
			report.Interstitials++
		}

		if step.Item.Category == runCategory {
			runLength += step.Length
		} else {
			if runLength > longest[runCategory] {
				longest[runCategory] = runLength
			}
			runCategory, runLength = step.Item.Category, step.Length
		}

		for _, rule := range step.Decision.Relaxed {
			report.Relaxations[rule]++
		}
	}
	if runLength > longest[runCategory] {
		longest[runCategory] = runLength
	}

	for category, aired := range byCategory {
		entry := SimCategory{Category: category, Minutes: int(aired.Minutes())}
		if total > 0 {
			entry.Percent = int(float64(aired)/float64(total)*100 + 0.5)
		}
		report.Categories = append(report.Categories, entry)
	}
	sort.SliceStable(report.Categories, func(i, j int) bool {
		return report.Categories[i].Minutes > report.Categories[j].Minutes
	})
	report.Sources = namedTotals(bySource)
	report.Creators = namedTotals(byCreator)
	for category, run := range longest {
		if category == "" {
			continue
		}
		report.LongestRun = append(report.LongestRun, SimNamedTotal{
			Name: string(category), Minutes: int(run.Minutes()),
		})
	}
	sort.SliceStable(report.LongestRun, func(i, j int) bool {
		return report.LongestRun[i].Minutes > report.LongestRun[j].Minutes
	})

	report.Blocks = blockSpans(result.Steps)
	report.Anchors = anchorOutcomes(engine, result.Steps, from, to)
	report.Obligations = obligationOutcomes(engine, result.Steps, to)
	report.Breaks = breakOutcomes(result.Steps)
	return report
}

// obligationOutcomes measures how long the station made the listener wait for
// the things it owed them.
func obligationOutcomes(engine *Engine, steps []SimStep, to time.Time) SimObligations {
	out := SimObligations{}
	waits := []time.Duration{}
	surfaced := map[string]bool{}
	for _, step := range steps {
		if step.Decision.Selected == nil || !step.Decision.Selected.Owed {
			continue
		}
		ref := step.Decision.Selected.Ref
		if surfaced[ref] {
			continue
		}
		surfaced[ref] = true
		out.Surfaced++
		for _, owed := range step.Decision.Owed {
			if owed.Ref == ref {
				waits = append(waits, time.Duration(owed.AgeMins)*time.Minute)
				break
			}
		}
	}
	if engine.Obligations != nil {
		if remaining, err := engine.Obligations.List(context.Background(), to); err == nil {
			for _, obligation := range remaining {
				if obligation.Pending() {
					out.StillOwed++
				}
			}
		}
	}
	if len(waits) > 0 {
		sort.Slice(waits, func(i, j int) bool { return waits[i] < waits[j] })
		out.MedianWaitMinutes = int(waits[len(waits)/2].Minutes())
		out.SlowestWaitMinutes = int(waits[len(waits)-1].Minutes())
	}
	return out
}

// breakOutcomes measures what the separators came out as.
func breakOutcomes(steps []SimStep) SimBreaks {
	out := SimBreaks{}
	totalMinutes := 0
	totalItems := 0
	for _, step := range steps {
		// Only the opening item of a break carries the whole plan; the rest are
		// positions inside it.
		if step.Decision.Break == nil || step.Decision.Break.Position != 1 {
			continue
		}
		out.Count++
		totalMinutes += step.Decision.Break.Minutes
		totalItems += step.Decision.Break.Of
		if !step.Decision.Break.InRange {
			out.OutOfRange++
		}
	}
	if out.Count > 0 {
		out.MeanMinutes = float64(totalMinutes) / float64(out.Count)
		out.MeanItems = float64(totalItems) / float64(out.Count)
	}
	return out
}

func namedTotals(totals map[string]time.Duration) []SimNamedTotal {
	out := make([]SimNamedTotal, 0, len(totals))
	for name, aired := range totals {
		if name == "" {
			continue
		}
		out = append(out, SimNamedTotal{Name: name, Minutes: int(aired.Minutes())})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Minutes != out[j].Minutes {
			return out[i].Minutes > out[j].Minutes
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func blockSpans(steps []SimStep) []SimBlockSpan {
	out := []SimBlockSpan{}
	for _, step := range steps {
		blockID := step.Decision.BlockID
		if len(out) > 0 && out[len(out)-1].BlockID == blockID {
			out[len(out)-1].To = step.Ends
			out[len(out)-1].Items++
			continue
		}
		out = append(out, SimBlockSpan{
			BlockID: blockID,
			Label:   firstNonEmpty(step.Decision.BlockLabel, blockID),
			From:    step.At,
			To:      step.Ends,
			Items:   1,
		})
	}
	return out
}

// anchorOutcomes checks every appointment inside the run against what actually
// went to air.
//
// The number that matters is how LATE each one started. With makeNext the
// answer should be "by less than the length of one item", and if it is ever
// much more than that, forward fitting is not working — which is precisely the
// bug that used to present as a show being cut off mid-sentence.
func anchorOutcomes(engine *Engine, steps []SimStep, from, to time.Time) []SimAnchor {
	loc := engine.location()
	seen := map[string]bool{}
	out := []SimAnchor{}
	for cursor := from; cursor.Before(to); cursor = cursor.Add(6 * time.Hour) {
		timeline := BuildTimeline(engine.Plan, cursor, loc)
		for _, anchor := range timeline.Anchors {
			if anchor.Start.Before(from) || !anchor.Start.Before(to) {
				continue
			}
			key := anchor.BlockID + "\x00" + anchor.Start.Format(time.RFC3339)
			if seen[key] {
				continue
			}
			seen[key] = true
			outcome := SimAnchor{
				BlockID: anchor.BlockID,
				Label:   anchor.Label,
				Due:     anchor.Start,
				Missed:  true,
			}
			for _, step := range steps {
				if step.Decision.BlockID != anchor.BlockID {
					continue
				}
				// Overlapping the window, not starting inside it: an
				// appointment brought forward because the gap in front of it
				// had closed did go out, and reporting that as "55 minutes
				// late" because the first matching item began before the hour
				// is worse than useless.
				if !step.Ends.After(anchor.Start) || !step.At.Before(anchor.End) {
					continue
				}
				outcome.Missed = false
				outcome.StartedAt = step.At
				if late := step.At.Sub(anchor.Start); late > 0 {
					outcome.LateBy = late.Round(time.Second).String()
				} else if late < 0 {
					outcome.EarlyBy = (-late).Round(time.Second).String()
				}
				break
			}
			out = append(out, outcome)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Due.Before(out[j].Due) })
	return out
}

// Format renders a run the way somebody reading a terminal wants it.
func (r SimResult) Format(verbose bool) string {
	var b strings.Builder
	report := r.Report
	fmt.Fprintf(&b, "SIMULATED  %s → %s  (%d items", report.From.Format("2006-01-02 15:04"),
		report.To.Format("2006-01-02 15:04"), report.Items)
	if report.Interstitials > 0 {
		fmt.Fprintf(&b, ", %d separators", report.Interstitials)
	}
	fmt.Fprintf(&b, ")\n\n")

	b.WriteString("BLOCK TIMELINE\n")
	for _, span := range report.Blocks {
		fmt.Fprintf(&b, "  %s → %s  %-26s %d items\n",
			span.From.Format("Mon 15:04"), span.To.Format("15:04"), span.Label, span.Items)
	}

	b.WriteString("\nCATEGORY AIRTIME\n")
	for _, category := range report.Categories {
		fmt.Fprintf(&b, "  %-14s %5dm  %3d%%\n", category.Category, category.Minutes, category.Percent)
	}

	b.WriteString("\nLONGEST UNBROKEN RUN\n")
	for _, run := range report.LongestRun {
		fmt.Fprintf(&b, "  %-14s %5dm\n", run.Name, run.Minutes)
	}

	if len(report.Anchors) > 0 {
		b.WriteString("\nBOOKED SLOTS\n")
		for _, anchor := range report.Anchors {
			switch {
			case anchor.Missed:
				fmt.Fprintf(&b, "  %s  %-24s MISSED\n", anchor.Due.Format("Mon 15:04"), anchor.Label)
			case anchor.LateBy != "":
				fmt.Fprintf(&b, "  %s  %-24s started %s late\n", anchor.Due.Format("Mon 15:04"), anchor.Label, anchor.LateBy)
			case anchor.EarlyBy != "":
				fmt.Fprintf(&b, "  %s  %-24s started %s early (nothing fitted the gap)\n",
					anchor.Due.Format("Mon 15:04"), anchor.Label, anchor.EarlyBy)
			default:
				fmt.Fprintf(&b, "  %s  %-24s on time\n", anchor.Due.Format("Mon 15:04"), anchor.Label)
			}
		}
	}

	if report.Obligations.Surfaced > 0 || report.Obligations.StillOwed > 0 {
		b.WriteString("\nOWED TO THE LISTENER\n")
		fmt.Fprintf(&b, "  surfaced     %d\n", report.Obligations.Surfaced)
		fmt.Fprintf(&b, "  still owed   %d\n", report.Obligations.StillOwed)
		if report.Obligations.Surfaced > 0 {
			fmt.Fprintf(&b, "  waited       %dm median, %dm worst\n",
				report.Obligations.MedianWaitMinutes, report.Obligations.SlowestWaitMinutes)
		}
	}

	if report.Breaks.Count > 0 {
		b.WriteString("\nBREAKS\n")
		fmt.Fprintf(&b, "  %d · mean %.1fm over %.1f items", report.Breaks.Count,
			report.Breaks.MeanMinutes, report.Breaks.MeanItems)
		if report.Breaks.OutOfRange > 0 {
			fmt.Fprintf(&b, " · %d outside the accepted range", report.Breaks.OutOfRange)
		}
		b.WriteString("\n")
	}

	b.WriteString("\nSOURCE AIRTIME\n")
	for _, source := range topN(report.Sources, 12) {
		fmt.Fprintf(&b, "  %-34s %5dm\n", truncate(source.Name, 34), source.Minutes)
	}
	if len(report.Creators) > 0 {
		b.WriteString("\nCREATOR AIRTIME\n")
		for _, creator := range topN(report.Creators, 12) {
			fmt.Fprintf(&b, "  %-34s %5dm\n", truncate(creator.Name, 34), creator.Minutes)
		}
	}

	fmt.Fprintf(&b, "\nSEPARATION   %d back-to-back same-creator items\n", report.BackToBackCreator)
	for _, when := range report.BackToBackWhen {
		fmt.Fprintf(&b, "   %s\n", when)
	}
	if len(report.Relaxations) > 0 {
		parts := make([]string, 0, len(report.Relaxations))
		for rule, count := range report.Relaxations {
			parts = append(parts, fmt.Sprintf("%s ×%d", rule, count))
		}
		sort.Strings(parts)
		fmt.Fprintf(&b, "RELAXED      %s\n", strings.Join(parts, ", "))
	}
	if report.Gaps > 0 {
		fmt.Fprintf(&b, "DEAD AIR     %d moments with nothing to play\n", report.Gaps)
		for _, gap := range r.Gaps {
			fmt.Fprintf(&b, "   %s  %s\n", gap.At.Format("Mon 15:04"), gap.Reason)
		}
	}

	if verbose {
		b.WriteString("\nRUNNING ORDER\n")
		for _, step := range r.Steps {
			fmt.Fprintf(&b, "  %s  %-9s %-46s %s\n",
				step.At.Format("Mon 15:04"),
				step.Length.Round(time.Minute),
				truncate(step.Item.Title, 46),
				step.Item.Category)
		}
	}
	return b.String()
}

// ExplainStep renders the full reasoning for one item in a run.
func (r SimResult) ExplainStep(index int) string {
	if index < 0 || index >= len(r.Steps) {
		return ""
	}
	return r.Steps[index].Decision.Explain()
}

func topN(items []SimNamedTotal, limit int) []SimNamedTotal {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}
