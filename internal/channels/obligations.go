package channels

import (
	"context"
	"sort"
	"strings"
	"time"
)

// A new episode is not "another candidate". It is something the station OWES
// the listener.
//
// The difference is not decorative. As a candidate, freshness is a number that
// competes and is forgotten; there is nothing to report, nothing to simulate,
// no way to say "these three are owed and one of them is nearly out of time",
// and no way to express that an airing at three in the morning did not really
// count. As a record, all of that is just reading a table.

// Tier is how much a station's owner cares about a source, S down to F.
//
// Deliberately a small ordinal rather than a number: "how important is this
// show" is a judgement people make in bands, and a free-floating float invites
// fiddling with 0.62 versus 0.64 as though that meant something.
type Tier string

const (
	TierS Tier = "S"
	TierA Tier = "A"
	TierB Tier = "B"
	TierC Tier = "C"
	TierD Tier = "D"
	TierE Tier = "E"
	TierF Tier = "F"
)

// DefaultTier is where a source sits when nobody has said. Middle of the range,
// so setting a tier on one show does not implicitly demote everything else.
const DefaultTier = TierC

var tierValues = map[Tier]float64{TierS: 6, TierA: 5, TierB: 4, TierC: 3, TierD: 2, TierE: 1, TierF: 0}

// ParseTier reads a tier, falling back to the default rather than erroring: a
// typo in a config field should not take a show off the air.
func ParseTier(raw string) Tier {
	tier := Tier(strings.ToUpper(strings.TrimSpace(raw)))
	if _, ok := tierValues[tier]; ok {
		return tier
	}
	return DefaultTier
}

// TierOf is a source's tier.
func TierOf(src Source) Tier {
	return ParseTier(stringFromConfig(src.Config, "tier"))
}

// Value is the tier as a number, for scoring.
func (t Tier) Value() float64 { return tierValues[ParseTier(string(t))] }

// ObligationState is where an obligation is in its life.
type ObligationState string

const (
	// ObligationPending is owed and not yet surfaced.
	ObligationPending ObligationState = "pending"
	// ObligationSatisfied means it reached the listener.
	ObligationSatisfied ObligationState = "satisfied"
	// ObligationExpired means it stopped being news before it got on air. The
	// episode is not lost — it simply returns to normal rotation, which is
	// where a three-week-old episode belongs.
	ObligationExpired ObligationState = "expired"
)

// Obligation is one thing the station owes the listener.
type Obligation struct {
	ChannelID   string    `json:"channelId,omitempty"`
	SourceID    string    `json:"sourceId"`
	SourceLabel string    `json:"sourceLabel,omitempty"`
	ItemRef     string    `json:"itemRef"`
	Title       string    `json:"title,omitempty"`
	Tier        Tier      `json:"tier"`
	PublishedAt time.Time `json:"publishedAt"`
	NoticedAt   time.Time `json:"noticedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	// Credit is accumulated exposure, 0..1. Not a boolean, because "did this
	// reach anybody" is a spectrum: an airing at 03:00 in a block worth nothing
	// adds nothing, a five-minute preemption adds a fraction, and both used to
	// count as a full airing and burn the episode.
	Credit float64         `json:"credit"`
	State  ObligationState `json:"state"`
	// SettleAt is how much credit this particular episode needs before the
	// station considers it surfaced — one airing for most things, two for the
	// shows whose new episodes are the reason the station exists. Stored per
	// row because the settle happens in SQL, and because re-rating a show
	// afterwards should not silently un-satisfy episodes already heard.
	SettleAt float64 `json:"settleAt,omitempty"`
	Airings  int     `json:"airings"`
}

// Pending reports whether this is still owed.
func (o Obligation) Pending() bool { return o.State == ObligationPending }

// satisfyThreshold is how much credit counts as "the listener has had their
// chance", when nothing more specific is asked for. One full airing in a block
// that counts for everything.
const satisfyThreshold = 1.0

// Target is how much credit settles THIS obligation.
//
// A single airing is the right default and the wrong answer for the handful of
// shows a station exists to play: go out for two hours and the one episode you
// were waiting for has been and gone. A second surfacing is the fix, and it has
// to be per-show rather than global — surfacing everything twice would spend
// most of a day repeating things.
//
// Zero means the row predates the policy, so it keeps the old behaviour.
func (o Obligation) Target() float64 {
	if o.SettleAt > 0 {
		return o.SettleAt
	}
	return satisfyThreshold
}

// SurfacingsFor is how many times this policy wants a tier's episodes aired.
func (p FreshnessPolicy) SurfacingsFor(tier Tier) float64 {
	if p.Surfacings == nil {
		return satisfyThreshold
	}
	if count, ok := p.Surfacings[string(ParseTier(string(tier)))]; ok && count > 0 {
		return float64(count)
	}
	return satisfyThreshold
}

// Urgency is how much the station wants to surface this right now.
//
// Three terms, and the relationship between them is the whole design:
//
//   - TIER dominates. One tier step is worth more than the entire recency
//     range, so an S-tier show published six hours ago beats a B-tier show
//     published ten minutes ago. Anything else means the loudest publisher wins
//     the morning.
//   - RECENCY orders within a tier. Among equals, newest first — that is what
//     "here is today's episode" means.
//   - EXPIRY lifts something about to stop being news. It is the last chance,
//     and it is weighted above recency on purpose so a nearly-dead obligation
//     can climb past a fresher one at the same tier.
func (o Obligation) Urgency(now time.Time, policy FreshnessPolicy) float64 {
	value := policy.tierSpread() * o.Tier.Value()
	window := o.ExpiresAt.Sub(o.PublishedAt)
	if window <= 0 {
		return value
	}
	age := now.Sub(o.PublishedAt)
	if age < 0 {
		age = 0
	}
	fraction := float64(age) / float64(window)
	if fraction > 1 {
		fraction = 1
	}
	value += policy.recencyWeight() * (1 - fraction)

	// Rises over the last stretch of the window rather than across all of it,
	// so "running out of time" means something.
	if fraction > policy.urgentFrom() {
		urgency := (fraction - policy.urgentFrom()) / (1 - policy.urgentFrom())
		value += policy.expiryWeight() * urgency
	}
	return value
}

// FreshnessPolicy tunes how obligations are ordered. Plan configuration.
type FreshnessPolicy struct {
	// TierSpread is what one tier step is worth. Larger makes tiers more
	// absolute; zero makes the queue pure recency, which is what the station
	// did before tiers existed.
	TierSpread float64 `json:"tierSpread,omitempty"`
	// RecencyWeight orders equals. ExpiryWeight lifts the nearly-expired.
	RecencyWeight float64 `json:"recencyWeight,omitempty"`
	ExpiryWeight  float64 `json:"expiryWeight,omitempty"`
	// UrgentFrom is the fraction of an obligation's life after which it starts
	// counting as running out of time.
	UrgentFrom float64 `json:"urgentFrom,omitempty"`
	// Surfacings is how many times a tier's episodes should reach the listener
	// before the station considers its job done, keyed by tier ("S", "A", …).
	// Absent or 1 is a single airing.
	//
	// Deliberately per tier and not global. Surfacing EVERYTHING twice doubles
	// the new content a day has to carry, which on a station whose feeds
	// already fill half the rotation leaves no room for anything else. Per tier
	// it costs almost nothing and covers the case that actually matters: the
	// two or three shows you would be annoyed to miss.
	Surfacings map[string]int `json:"surfacings,omitempty"`
}

func (p FreshnessPolicy) tierSpread() float64 {
	if p.TierSpread != 0 {
		return p.TierSpread
	}
	return 2.0
}

func (p FreshnessPolicy) recencyWeight() float64 {
	if p.RecencyWeight != 0 {
		return p.RecencyWeight
	}
	return 1.0
}

func (p FreshnessPolicy) expiryWeight() float64 {
	if p.ExpiryWeight != 0 {
		return p.ExpiryWeight
	}
	return 1.5
}

func (p FreshnessPolicy) urgentFrom() float64 {
	if p.UrgentFrom > 0 && p.UrgentFrom < 1 {
		return p.UrgentFrom
	}
	return 0.8
}

// ObligationStore is where obligations live between decisions.
//
// An interface for the same reason History is: the simulator runs three days of
// obligation lifecycle — created, surfaced, partially credited, satisfied,
// expired — without touching the real station's tables.
type ObligationStore interface {
	// List returns everything not yet expired, so the caller can see satisfied
	// ones too (they are why something is NOT being offered).
	List(ctx context.Context, now time.Time) ([]Obligation, error)
	// Notice records obligations the station has just become aware of. Existing
	// refs are left exactly as they are — noticing an episode twice must not
	// reset the credit it has already earned.
	Notice(ctx context.Context, obligations []Obligation, now time.Time) error
	// Credit adds exposure to one obligation and settles its state.
	Credit(ctx context.Context, itemRef string, credit float64, now time.Time) error
}

// ---- in-memory ---------------------------------------------------------

// MemoryObligations is an obligation store that never touches a database.
type MemoryObligations struct {
	byRef map[string]*Obligation
	order []string
}

// NewMemoryObligations builds an empty in-memory store.
func NewMemoryObligations() *MemoryObligations {
	return &MemoryObligations{byRef: map[string]*Obligation{}}
}

func (m *MemoryObligations) List(_ context.Context, now time.Time) ([]Obligation, error) {
	out := make([]Obligation, 0, len(m.order))
	for _, ref := range m.order {
		entry := m.byRef[ref]
		if entry == nil {
			continue
		}
		settle(entry, now)
		if entry.State == ObligationExpired {
			continue
		}
		out = append(out, *entry)
	}
	return out, nil
}

func (m *MemoryObligations) Notice(_ context.Context, obligations []Obligation, _ time.Time) error {
	for _, obligation := range obligations {
		if obligation.ItemRef == "" {
			continue
		}
		if existing, ok := m.byRef[obligation.ItemRef]; ok {
			// Refresh what describes the SHOW, never the obligation's own life.
			// A tier that cannot be applied to the episodes already waiting is
			// a rating with no effect.
			existing.Tier = obligation.Tier
			existing.Title = obligation.Title
			existing.SourceID = obligation.SourceID
			existing.SourceLabel = obligation.SourceLabel
			continue
		}
		stored := obligation
		if stored.State == "" {
			stored.State = ObligationPending
		}
		m.byRef[obligation.ItemRef] = &stored
		m.order = append(m.order, obligation.ItemRef)
	}
	return nil
}

func (m *MemoryObligations) Credit(_ context.Context, itemRef string, credit float64, now time.Time) error {
	entry, ok := m.byRef[itemRef]
	if !ok {
		return nil
	}
	entry.Credit += credit
	entry.Airings++
	settle(entry, now)
	return nil
}

// settle moves an obligation to whichever state its credit and clock imply.
func settle(entry *Obligation, now time.Time) {
	if entry.State == ObligationSatisfied {
		return
	}
	if entry.Credit >= entry.Target() {
		entry.State = ObligationSatisfied
		return
	}
	if !entry.ExpiresAt.IsZero() && !now.Before(entry.ExpiresAt) {
		entry.State = ObligationExpired
		return
	}
	entry.State = ObligationPending
}

// ---- the queue ---------------------------------------------------------

// ObligationQueue is what the station owes, most urgent first.
type ObligationQueue struct {
	Pending   []Obligation
	Satisfied []Obligation
	byRef     map[string]Obligation
}

// NewObligationQueue orders a set of obligations for one moment.
func NewObligationQueue(obligations []Obligation, now time.Time, policy FreshnessPolicy) ObligationQueue {
	queue := ObligationQueue{byRef: make(map[string]Obligation, len(obligations))}
	for _, obligation := range obligations {
		queue.byRef[obligation.ItemRef] = obligation
		if obligation.Pending() {
			queue.Pending = append(queue.Pending, obligation)
			continue
		}
		if obligation.State == ObligationSatisfied {
			queue.Satisfied = append(queue.Satisfied, obligation)
		}
	}
	sort.SliceStable(queue.Pending, func(i, j int) bool {
		left := queue.Pending[i].Urgency(now, policy)
		right := queue.Pending[j].Urgency(now, policy)
		if left != right {
			return left > right
		}
		return queue.Pending[i].ItemRef < queue.Pending[j].ItemRef
	})
	return queue
}

// Owes reports whether a specific item is still owed.
func (q ObligationQueue) Owes(itemRef string) bool {
	obligation, ok := q.byRef[itemRef]
	return ok && obligation.Pending()
}

// Get returns the obligation for an item, if there is one.
func (q ObligationQueue) Get(itemRef string) (Obligation, bool) {
	obligation, ok := q.byRef[itemRef]
	return obligation, ok
}

// Len is how many things the station currently owes.
func (q ObligationQueue) Len() int { return len(q.Pending) }

// Rank is where an item sits in the queue, 0 being most urgent. Returns -1 for
// anything not owed.
func (q ObligationQueue) Rank(itemRef string) int {
	for index, obligation := range q.Pending {
		if obligation.ItemRef == itemRef {
			return index
		}
	}
	return -1
}
