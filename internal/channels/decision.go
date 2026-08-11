package channels

import (
	"fmt"
	"strings"
	"time"
)

// A Decision is the full account of one choice.
//
// It exists because of a specific, repeated experience: the station plays
// something baffling, and from the outside every possible cause looks
// identical. Was a slot on air? Was the episode considered and rejected, or
// never enumerated? Did separation rule it out, or did it simply score badly?
// Guessing at that cost entire evenings. Now every one of those questions has a
// recorded answer, and "why the hell did SamoRadio play this" is a lookup.
type Decision struct {
	At        time.Time `json:"at"`
	ChannelID string    `json:"channelId"`
	Timezone  string    `json:"timezone,omitempty"`

	// The programming state: what the station thought it was doing.
	BlockID     string    `json:"blockId"`
	BlockLabel  string    `json:"blockLabel,omitempty"`
	EnteredAt   time.Time `json:"enteredAt,omitempty"`
	EntryReason string    `json:"entryReason,omitempty"`
	ExitReason  string    `json:"exitReason,omitempty"`

	// The timeline: what is booked, and how much room there is before it.
	NextAnchor    *AnchorSummary `json:"nextAnchor,omitempty"`
	WindowSeconds int            `json:"windowSeconds,omitempty"`
	// TargetSeconds is what the block wanted the next item to be, if it had an
	// opinion.
	TargetSeconds int `json:"targetSeconds,omitempty"`

	// The format: what each category is owed.
	Targets []CategoryStatus `json:"targets,omitempty"`
	Limits  []LimitStatus    `json:"limits,omitempty"`

	// Want is what this position in the block's cycle called for, when it was
	// anything other than ordinary programming.
	Want string `json:"want,omitempty"`
	// Owed is what the station currently owes the listener, most urgent first.
	Owed []OwedSummary `json:"owed,omitempty"`
	// Break describes the separator this item is part of, if it is.
	Break *BreakSummary `json:"break,omitempty"`

	// The selection.
	Considered int                `json:"considered"`
	Candidates []CandidateSummary `json:"candidates,omitempty"`
	Rejected   []Rejection        `json:"rejected,omitempty"`
	Relaxed    []string           `json:"relaxed,omitempty"`
	Selected   *SelectedSummary   `json:"selected,omitempty"`
	Note       string             `json:"note,omitempty"`
	Error      string             `json:"error,omitempty"`
}

// OwedSummary is one outstanding obligation, as the decision saw it.
type OwedSummary struct {
	Ref     string  `json:"ref"`
	Title   string  `json:"title"`
	Source  string  `json:"source,omitempty"`
	Tier    string  `json:"tier"`
	Credit  float64 `json:"credit"`
	AgeMins int     `json:"ageMinutes"`
	Expires string  `json:"expiresIn,omitempty"`
	Urgency float64 `json:"urgency"`
}

// AnchorSummary is the next appointment, as the decision saw it.
type AnchorSummary struct {
	BlockID string    `json:"blockId"`
	Label   string    `json:"label"`
	Start   time.Time `json:"start"`
	At      string    `json:"at"`
	In      string    `json:"in"`
	Policy  string    `json:"policy"`
}

// CategoryStatus is one category's target against what it actually got.
type CategoryStatus struct {
	Category      CategoryID `json:"category"`
	TargetPercent int        `json:"targetPercent"`
	ActualPercent int        `json:"actualPercent"`
	AiredMinutes  int        `json:"airedMinutes"`
}

// LimitStatus is how close a block limit is to biting.
type LimitStatus struct {
	Category     CategoryID `json:"category"`
	RunMinutes   int        `json:"runMinutes"`
	MaxMinutes   int        `json:"maxMinutes"`
	Exceeded     bool       `json:"exceeded"`
	HeadroomMins int        `json:"headroomMinutes"`
}

// CandidateSummary is one thing that was in the running.
type CandidateSummary struct {
	Ref       string      `json:"ref"`
	Title     string      `json:"title"`
	Source    string      `json:"source,omitempty"`
	Category  string      `json:"category,omitempty"`
	Minutes   int         `json:"minutes,omitempty"`
	Score     float64     `json:"score"`
	Contender bool        `json:"contender,omitempty"`
	Terms     []ScoreTerm `json:"terms,omitempty"`
}

// SelectedSummary is what actually went on air, and why.
type SelectedSummary struct {
	Ref      string      `json:"ref"`
	Title    string      `json:"title"`
	SourceID string      `json:"sourceId,omitempty"`
	Category string      `json:"category,omitempty"`
	Score    float64     `json:"score,omitempty"`
	Reason   string      `json:"reason,omitempty"`
	Terms    []ScoreTerm `json:"terms,omitempty"`
	// Owed marks this as something the station was surfacing because it owed
	// it, rather than because the rotation reached for it.
	Owed bool `json:"owed,omitempty"`
}

// maxRecordedRejections bounds the record. Two hundred candidates all failing
// the same separation rule is one fact, not two hundred, and an unbounded list
// would make every decision row enormous for no extra insight.
const maxRecordedRejections = 24

func capRejections(rejections []Rejection) []Rejection {
	if len(rejections) <= maxRecordedRejections {
		return rejections
	}
	return rejections[:maxRecordedRejections]
}

// maxRecordedCandidates is how much of the ranking is kept.
const maxRecordedCandidates = 8

func summariseCandidates(scored []ScoredCandidate, contenders int) []CandidateSummary {
	limit := len(scored)
	if limit > maxRecordedCandidates {
		limit = maxRecordedCandidates
	}
	out := make([]CandidateSummary, 0, limit)
	for index := 0; index < limit; index++ {
		candidate := scored[index]
		out = append(out, CandidateSummary{
			Ref:       candidate.Candidate.Ref,
			Title:     candidate.Candidate.Title,
			Source:    candidate.Candidate.SourceID,
			Category:  string(candidate.Candidate.Category),
			Minutes:   int(candidate.Candidate.Duration.Minutes()),
			Score:     round2(candidate.Total),
			Contender: index < contenders,
			Terms:     roundTerms(candidate.Terms),
		})
	}
	return out
}

func round2(value float64) float64 {
	return float64(int(value*100+copySign(0.5, value))) / 100
}

func copySign(magnitude, sign float64) float64 {
	if sign < 0 {
		return -magnitude
	}
	return magnitude
}

func roundTerms(terms []ScoreTerm) []ScoreTerm {
	out := make([]ScoreTerm, 0, len(terms))
	for _, term := range terms {
		out = append(out, ScoreTerm{Name: term.Name, Value: round2(term.Value), Weight: term.Weight})
	}
	return out
}

// applyIntent copies the programming state into the record.
func (d *Decision) applyIntent(intent ProgrammingIntent, timeline Timeline) {
	d.BlockID = intent.Block.ID
	d.BlockLabel = intent.BlockLabel
	d.EnteredAt = intent.EnteredAt
	d.EntryReason = intent.EntryReason
	d.ExitReason = intent.ExitReason
	d.WindowSeconds = int(intent.Window.Seconds())
	d.TargetSeconds = int(intent.TargetDuration.Seconds())
	if timeline.Next != nil {
		policy := timeline.Next.Policy
		if policy == "" {
			policy = StartMakeNext
		}
		d.NextAnchor = &AnchorSummary{
			BlockID: timeline.Next.BlockID,
			Label:   timeline.Next.Label,
			Start:   timeline.Next.Start,
			At:      timeline.Next.Start.Format("15:04"),
			In:      timeline.Next.Start.Sub(timeline.Now).Round(time.Minute).String(),
			Policy:  string(policy),
		}
	}
	for _, limit := range intent.Limits {
		d.Limits = append(d.Limits, LimitStatus{
			Category:     limit.Category,
			RunMinutes:   int(limit.Run.Minutes()),
			MaxMinutes:   int(limit.Max.Minutes()),
			Exceeded:     limit.Exceeded(),
			HeadroomMins: int(limit.Remaining().Minutes()),
		})
	}
}

// applyOwed records the obligation queue as it stood.
//
// Kept short: the top of the queue is what explains a choice, and a station
// that has noticed forty episodes overnight should not write forty rows into
// every decision for the next three days.
func (d *Decision) applyOwed(queue ObligationQueue, now time.Time, policy FreshnessPolicy) {
	const limit = 6
	for index, obligation := range queue.Pending {
		if index >= limit {
			break
		}
		summary := OwedSummary{
			Ref:     obligation.ItemRef,
			Title:   obligation.Title,
			Source:  obligation.SourceLabel,
			Tier:    string(obligation.Tier),
			Credit:  round2(obligation.Credit),
			AgeMins: int(now.Sub(obligation.PublishedAt).Minutes()),
			Urgency: round2(obligation.Urgency(now, policy)),
		}
		if !obligation.ExpiresAt.IsZero() {
			summary.Expires = obligation.ExpiresAt.Sub(now).Round(time.Minute).String()
		}
		d.Owed = append(d.Owed, summary)
	}
}

// applyBalance records what each category is owed against what it got.
func (d *Decision) applyBalance(targets map[CategoryID]float64, airtime AirtimeWindow) {
	for category, target := range targets {
		status := CategoryStatus{
			Category:      category,
			TargetPercent: int(target*100 + 0.5),
			AiredMinutes:  int(airtime.ByCategory[category].Minutes()),
		}
		if airtime.Total > 0 {
			status.ActualPercent = int(float64(airtime.ByCategory[category])/float64(airtime.Total)*100 + 0.5)
		}
		d.Targets = append(d.Targets, status)
	}
	sortCategoryStatus(d.Targets)
}

func sortCategoryStatus(items []CategoryStatus) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Category < items[j-1].Category; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// Explain renders the decision the way a person would want it read out.
//
// Used by the simulator and by `radio-sim`, and deliberately the same text the
// API serves, so what you read on the terminal is what the browser shows.
func (d Decision) Explain() string {
	var b strings.Builder
	fmt.Fprintf(&b, "DECISION  %s  %s\n", d.At.Format("2006-01-02 15:04:05"), d.Timezone)
	fmt.Fprintf(&b, "  block        %s", firstNonEmpty(d.BlockLabel, d.BlockID))
	if !d.EnteredAt.IsZero() {
		fmt.Fprintf(&b, "  (entered %s)", d.EnteredAt.Format("15:04"))
	}
	b.WriteString("\n")
	if d.EntryReason != "" {
		fmt.Fprintf(&b, "  because      %s\n", d.EntryReason)
	}
	if d.ExitReason != "" {
		fmt.Fprintf(&b, "  ends         %s\n", d.ExitReason)
	}
	if d.NextAnchor != nil {
		fmt.Fprintf(&b, "  next slot    %s at %s (in %s, %s)\n",
			d.NextAnchor.Label, d.NextAnchor.At, d.NextAnchor.In, d.NextAnchor.Policy)
	}
	if d.WindowSeconds > 0 {
		fmt.Fprintf(&b, "  room         %s\n", (time.Duration(d.WindowSeconds) * time.Second).Round(time.Minute))
	}
	for _, target := range d.Targets {
		fmt.Fprintf(&b, "  format       %-10s %d%% target / %d%% actual (%dm)\n",
			target.Category, target.TargetPercent, target.ActualPercent, target.AiredMinutes)
	}
	for _, limit := range d.Limits {
		fmt.Fprintf(&b, "  limit        %s run %dm of %dm%s\n",
			limit.Category, limit.RunMinutes, limit.MaxMinutes, exceededSuffix(limit.Exceeded))
	}
	if d.Note != "" {
		fmt.Fprintf(&b, "  note         %s\n", d.Note)
	}
	fmt.Fprintf(&b, "  considered   %d\n", d.Considered)
	for _, candidate := range d.Candidates {
		marker := " "
		if candidate.Contender {
			marker = "*"
		}
		fmt.Fprintf(&b, "   %s %-7.2f %-40s %s\n", marker, candidate.Score, truncate(candidate.Title, 40), termLine(candidate.Terms))
	}
	for _, rejection := range d.Rejected {
		fmt.Fprintf(&b, "   x %-48s %s: %s\n", truncate(rejection.Title, 48), rejection.Rule, rejection.Reason)
	}
	if len(d.Relaxed) > 0 {
		fmt.Fprintf(&b, "  relaxed      %s\n", strings.Join(d.Relaxed, ", "))
	}
	if d.Selected != nil {
		fmt.Fprintf(&b, "  SELECTED     %s — %s\n", d.Selected.Title, d.Selected.Reason)
	}
	if d.Error != "" {
		fmt.Fprintf(&b, "  ERROR        %s\n", d.Error)
	}
	return b.String()
}

func exceededSuffix(exceeded bool) string {
	if exceeded {
		return "  (exceeded)"
	}
	return ""
}

func termLine(terms []ScoreTerm) string {
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		parts = append(parts, fmt.Sprintf("%s %.2f", term.Name, term.Contribution()))
	}
	return strings.Join(parts, " · ")
}

func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}
