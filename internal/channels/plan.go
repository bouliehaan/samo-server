package channels

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file is the station's programming language.
//
// Everything the old scheduler knew about what a radio station is — that talk
// and music split the hour, that ninety minutes is too long to talk for, that
// eight in the morning is when somebody wakes up — was a constant in the
// engine. That makes exactly one station, and it is not a station the owner can
// change. Here those are all facts about a PLAN: a document that says which
// content exists, what the day is shaped like, and what should be true of the
// running order. The engine below it knows how to program radio; the plan says
// what radio to program.
//
// A channel with no plan is not a special case. DerivePlan builds the plan its
// existing sources and schedule rules already describe, so an untouched channel
// keeps behaving exactly as it did and the whole model is reachable by editing
// that plan rather than by migrating anything.

// PlanVersion is the current plan schema version. Stored on the document so a
// later change can migrate old plans instead of guessing.
const PlanVersion = 1

// Plan is a channel's whole programming configuration.
type Plan struct {
	Version int `json:"version"`
	// Seed makes selection reproducible. Zero means "seed from the channel id",
	// which is stable across restarts but different per channel — a station
	// should not make the same choices as its neighbour.
	Seed int64 `json:"seed,omitempty"`

	Categories []CategoryDef `json:"categories"`
	Pools      []Pool        `json:"pools"`
	Blocks     []Block       `json:"blocks"`

	// LongForm rations the enormous. A six-hour episode is a high-commitment
	// choice, and it should be possible, rare, and never a surprise that ruins
	// a booked show.
	LongForm LongFormPolicy `json:"longForm,omitempty"`

	// MinItem is the floor on what counts as programming at all.
	//
	// The mirror of LongForm: that rations the enormous, this excludes the
	// trivial. Feeds carry things that are not episodes — a sixty-second "the
	// show has ended" post, a trailer, a "we are on break until January" —
	// and to the scheduler they look like perfectly good short items, which
	// makes them ideal for exactly the gap before a booked show. So the one
	// slot most likely to be filled with an announcement is the one right
	// before something you were looking forward to.
	//
	// Items with no known duration are never excluded: unmeasured is not the
	// same as short, and guessing would drop real episodes.
	MinItem string `json:"minItem,omitempty"`

	Separation SeparationPolicy `json:"separation"`
	Horizons   Horizons         `json:"horizons"`
	Selection  SelectionPolicy  `json:"selection"`
	Freshness  FreshnessPolicy  `json:"freshness,omitempty"`

	// ListeningDay is the hours in which airing something means somebody could
	// plausibly have heard it. It is the DEFAULT exposure schedule: a block may
	// override it outright, and a station whose day is not a contiguous block
	// of hours should do exactly that.
	//
	// It lives here rather than being a fact about the channel because it is a
	// programming decision — "these are the hours worth spending a new episode
	// on" — and every other programming decision moved into the plan.
	ListeningDay *DaySpec `json:"listeningDay,omitempty"`

	// UnderrunPool is what fills a gap too small for real programming before a
	// hard anchor. Empty means "start the anchor early", which is nearly always
	// the better answer: forty seconds of filler is worse radio than a show
	// that begins forty seconds sooner.
	UnderrunPool string `json:"underrunPool,omitempty"`
}

// CategoryDef is one of the station's own programming categories.
//
// Deliberately an open set. "talk" and "music" are the default because that is
// the split most stations notice, not because the engine knows what they mean —
// a station may run comedy, audiobook, old-time-radio and sports instead, and
// nothing below this line compares a category to a literal.
type CategoryDef struct {
	ID    CategoryID `json:"id"`
	Label string     `json:"label,omitempty"`
	// Target is this category's share of airtime, 0..1. Targets are normalised
	// against each other, so they do not have to sum to exactly one.
	Target float64 `json:"target"`
}

// CategoryID is a plan-defined category name.
type CategoryID string

// Pool is a named, reusable set of content.
//
// Resolved at decision time rather than frozen: a pool naming a podcast source
// means "whatever that show currently has", so adding an episode changes what
// the pool contains without anybody re-saving anything.
type Pool struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	// SourceIDs is a hand-picked list. Use it for a pool that is deliberately
	// exactly these things — a booked show, a specific stopset.
	SourceIDs []string `json:"sourceIds"`
	// Match is a live rule instead of a list, and it is what a rotation pool
	// should almost always use.
	//
	// A frozen list of ids is a snapshot of the library at the moment somebody
	// pressed save. Add a podcast afterwards and it joins no pool, so the
	// scheduler cannot see it — while every other screen cheerfully reports it
	// as enabled, and the obligation queue reports that the station owes you
	// its episodes. There is no error anywhere and the show simply never plays.
	// That is not a configuration mistake a person can be expected to catch; it
	// is a model that quietly loses content.
	Match *PoolMatch `json:"match,omitempty"`
}

// PoolMatch selects sources by what they ARE rather than by name.
type PoolMatch struct {
	Category CategoryID `json:"category,omitempty"`
	Role     string     `json:"role,omitempty"`
	Kind     string     `json:"kind,omitempty"`
}

// Selects reports whether a source belongs to this pool.
func (p Pool) Selects(src Source) bool {
	for _, id := range p.SourceIDs {
		if id == src.ID {
			return true
		}
	}
	if p.Match == nil {
		return false
	}
	// A booked show is not rotation inventory. RoleShow means "only ever plays
	// when a schedule rule calls for it", and a rule names its source
	// explicitly, so a match pool must never sweep one in: a category match
	// would otherwise put every scheduled station — the news hour, the
	// overnight lofi feed, the shortwave relay — into the general rotation,
	// where they turn up at random times as though they were podcasts.
	//
	// Naming a show in SourceIDs still selects it, which is how an anchored
	// block reaches its own show. Only the RULE-shaped selection is narrowed.
	if src.Role == RoleShow && p.Match.Role != RoleShow {
		return false
	}
	if p.Match.Category != "" && SourceCategory(src) != p.Match.Category {
		return false
	}
	if p.Match.Role != "" && src.Role != p.Match.Role {
		return false
	}
	if p.Match.Kind != "" && src.Kind != p.Match.Kind {
		return false
	}
	return true
}

// Resolve is every source this pool currently contains.
func (p Pool) Resolve(sources []Source) []Source {
	out := make([]Source, 0, len(sources))
	for _, src := range sources {
		if p.Selects(src) {
			out = append(out, src)
		}
	}
	return out
}

// UnreachableSources is every enabled source no pool can select.
//
// A source nothing can reach is not "configured oddly", it is content the
// station will never play while telling you it is enabled. The plan is refused
// on save when this is non-empty, and the status endpoint reports it for plans
// that were saved before the rule existed.
func (p Plan) UnreachableSources(sources []Source) []Source {
	out := []Source{}
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		// A booked show reaches the air through its own anchored block; it is
		// not expected to be in a rotation pool.
		if src.Role == RoleShow {
			continue
		}
		reachable := false
		for _, pool := range p.Pools {
			if pool.Selects(src) {
				reachable = true
				break
			}
		}
		if !reachable {
			out = append(out, src)
		}
	}
	return out
}

// PoolRef is a block's use of a pool, with how much it prefers it.
type PoolRef struct {
	Pool   string  `json:"pool"`
	Weight float64 `json:"weight,omitempty"`
}

// poolWeight is a pool's preference inside a block, defaulting to 1 so an
// unweighted plan is a plan where every pool is equal rather than one where
// every pool is ignored.
func poolWeight(ref PoolRef) float64 {
	if ref.Weight <= 0 {
		return 1
	}
	return ref.Weight
}

// Block is the character of the station for a span or a state.
//
// The unit is deliberately not an hour. A format clock assumes items are
// interchangeable and about three and a half minutes long; this station's items
// run from a thirty-second ident to a six-hour episode, and an hour grid would
// be a fiction the scheduler had to violate constantly. A block says what the
// station IS right now and lets the running order be generated.
type Block struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	// Default marks the block everything falls back to. Exactly one block must
	// have it, and it must have no entry condition — that is what guarantees
	// the station always has somewhere to be.
	Default bool `json:"default,omitempty"`

	Enter BlockEntry `json:"enter"`
	Exit  BlockExit  `json:"exit"`
	// Next is the block to move to when this one exits. Empty falls back to the
	// default block.
	Next string `json:"next,omitempty"`

	Pools []PoolRef `json:"pools"`
	// Balance overrides the plan's category targets while this block is on air.
	// A night block can be almost entirely spoken word without changing what
	// the rest of the day is aiming at.
	Balance map[CategoryID]float64 `json:"balance,omitempty"`

	Limits BlockLimits `json:"limits,omitempty"`

	// Exposure is how much playing something HERE counts toward the station's
	// obligation to surface it, 0..1. Nil falls back to the listening day.
	//
	// This is the general form of "an episode aired at 03:00 has not reached
	// anyone". Zero means airing it here spends nothing; a half means it partly
	// counts; one means the listener has had their chance. What constitutes a
	// meaningful airing is a property of the programming, not of the clock, and
	// a station whose attention pattern is not "awake between these hours" can
	// say so block by block.
	Exposure *float64 `json:"exposure,omitempty"`

	// Breaks is how this block separates its programming. Nil means it does
	// not: a night block of long-form does not want a stopset every twenty
	// minutes.
	Breaks *BreakPolicy `json:"breaks,omitempty"`

	// LongForm overrides the station's long-form policy while this block is on
	// air, which is how "a giant is welcome on a quiet afternoon and never in
	// the middle of the new-episode cycle" gets said.
	//
	// The condition Jacob actually wants is not a timer, it is "there is a big
	// enough hole AND nothing is owed" — and a block already MEANS "nothing is
	// owed", so the eligibility belongs on the block rather than in another
	// rule. The hole takes care of itself: a four-hour episode simply does not
	// fit a two-hour gap, and the window constraint never relaxes.
	LongForm *LongFormPolicy `json:"longForm,omitempty"`

	// Pattern is a repeating sequence of intents, for a block whose shape is a
	// cycle rather than a rotation: "a new episode, a break, a new episode, a
	// break, until there are no new episodes left".
	//
	// The one clock-like structure worth having. It says nothing about how long
	// each step takes, which is the part a broadcast clock gets wrong.
	Pattern []PatternStep `json:"pattern,omitempty"`
}

// PatternStep is one position in a block's cycle.
type PatternStep struct {
	// Want is what this position is for.
	Want WantKind `json:"want"`
}

// WantKind is the kind of programming a position calls for.
type WantKind string

const (
	// WantFill is ordinary programming from the block's pools. The default.
	WantFill WantKind = "fill"
	// WantObligation is something the station owes the listener — and nothing
	// else. A position that finds nothing owed falls through to fill rather
	// than leaving a hole.
	WantObligation WantKind = "obligation"
	// WantBreak is a separator, assembled by the block's break policy.
	WantBreak WantKind = "break"
)

// DaySpec is a window of wall-clock hours.
type DaySpec struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// ---- breaks ------------------------------------------------------------

// BreakPolicy is how a block assembles a separator.
//
// A break is a unit of programming, not one item on a clock. "Play two songs"
// is not a specification: two fifteen-minute songs is a half-hour break and two
// thirty-second songs is a minute. Both a count range and a duration range are
// hard, and the planner finds the combination that satisfies them.
type BreakPolicy struct {
	// Between is which categories a break separates. Empty means all of them.
	Between []CategoryID `json:"between,omitempty"`
	// Target is what a break should ideally be.
	Target BreakSize `json:"target"`
	// Accept bounds what is tolerable. Outside it, the planner takes the
	// closest it can and says so.
	Accept BreakRange `json:"accept,omitempty"`
	// Elements are what a break is made of, in order.
	Elements []BreakElement `json:"elements"`
	// MinGap is the least time between breaks.
	MinGap string `json:"minGap,omitempty"`
}

// BreakSize is a break's intended shape.
type BreakSize struct {
	Duration string `json:"duration,omitempty"`
	Items    int    `json:"items,omitempty"`
}

// BreakRange is the tolerable shape, as [min, max] pairs.
type BreakRange struct {
	Duration []string `json:"duration,omitempty"`
	Items    []int    `json:"items,omitempty"`
}

// BreakElement is one ingredient of a break.
type BreakElement struct {
	Pool string `json:"pool"`
	// Count is [min, max] items from this pool. An element whose pool is empty
	// simply contributes nothing — which is why "no commercials configured"
	// needs no special case anywhere.
	Count []int `json:"count,omitempty"`
	// Position pins an element to the start or end of the break, for the ident
	// that always opens it.
	Position string `json:"position,omitempty"`
	// Fill marks the elastic element: the one that absorbs whatever duration is
	// left after the fixed parts, inside its own count range.
	Fill bool `json:"fill,omitempty"`
}

func (b BreakPolicy) minGap() time.Duration { return durationOr(b.MinGap, 0) }

func (b BreakPolicy) targetDuration() time.Duration { return durationOr(b.Target.Duration, 0) }

// separates reports whether this policy puts a break between two items of the
// given category.
func (b BreakPolicy) separates(category CategoryID) bool {
	if len(b.Between) == 0 {
		return true
	}
	for _, want := range b.Between {
		if want == category {
			return true
		}
	}
	return false
}

// countRange is an element's [min, max], defaulting to exactly one.
func (e BreakElement) countRange() (int, int) {
	switch len(e.Count) {
	case 0:
		return 1, 1
	case 1:
		return e.Count[0], e.Count[0]
	default:
		low, high := e.Count[0], e.Count[1]
		if low > high {
			low, high = high, low
		}
		if low < 0 {
			low = 0
		}
		return low, high
	}
}

// itemRange is the acceptable number of items in a whole break.
func (r BreakRange) itemRange(fallbackTarget int) (int, int) {
	if len(r.Items) >= 2 {
		low, high := r.Items[0], r.Items[1]
		if low > high {
			low, high = high, low
		}
		return low, high
	}
	if fallbackTarget > 0 {
		return fallbackTarget, fallbackTarget
	}
	return 1, 4
}

// durationRange is the acceptable total length of a whole break. Zero max means
// unbounded.
func (r BreakRange) durationRange() (time.Duration, time.Duration) {
	if len(r.Duration) >= 2 {
		low := durationOr(r.Duration[0], 0)
		high := durationOr(r.Duration[1], 0)
		if high > 0 && low > high {
			low, high = high, low
		}
		return low, high
	}
	return 0, 0
}

// BlockEntry says when a block takes over. A block may be time-anchored,
// relationship-anchored, conditional, or any combination.
type BlockEntry struct {
	// At is a wall clock time in the channel's zone, "HH:MM". With Hard set it
	// becomes an appointment the rest of the schedule programs around.
	At   string `json:"at,omitempty"`
	Days string `json:"days,omitempty"` // "*", "mon-fri", "sat,sun", "mon,wed"
	Hard bool   `json:"hard,omitempty"`
	// Start is what to do when the appointment arrives and something is already
	// playing. Rivendell's three answers, because they are the three answers.
	Start StartPolicy `json:"start,omitempty"`
	Grace string      `json:"grace,omitempty"` // for waitUpTo

	// After names a block whose exit hands over to this one. This is what makes
	// a sequence survive being re-timed: anchor the news at 07:00 and the music
	// transition to "after the news", and moving the news moves everything.
	After string `json:"after,omitempty"`

	// When is an extra condition, from a small closed vocabulary. See
	// ParseCondition.
	When string `json:"when,omitempty"`

	// MaxPerDay caps how often this block may be entered in one listening day.
	// Zero means no cap.
	//
	// What makes a flex block possible. A condition like "window < 45m" is true
	// at the tail of every booked slot, so a block that only says WHEN it may
	// run will run at every one of them; the station needs a way to say "this
	// is a thing I do occasionally, when the schedule asks for it" without
	// pinning it to a clock time. Pinning it to a clock time is what a fixed
	// music hour is, and a fixed hour is an appointment: it fragments the day
	// into stretches too short for a long episode, which is the whole problem
	// it was meant to help with.
	MaxPerDay int `json:"maxPerDay,omitempty"`
}

// StartPolicy is what happens when a hard anchor is due and something is on.
type StartPolicy string

const (
	// StartMakeNext lets the current item finish first. With candidates already
	// filtered to what fits before the anchor, this almost never means a late
	// start, and it means nothing is ever cut off mid-sentence.
	StartMakeNext StartPolicy = "makeNext"
	// StartImmediately cuts in on the minute.
	StartImmediately StartPolicy = "startImmediately"
	// StartWaitUpTo waits for the current item, but cuts it off past Grace.
	StartWaitUpTo StartPolicy = "waitUpTo"
)

// BlockExit says what ends a block. The first condition that fires wins.
type BlockExit struct {
	At           string `json:"at,omitempty"`       // "HH:MM"
	Duration     string `json:"duration,omitempty"` // "12m"
	Tolerance    string `json:"tolerance,omitempty"`
	AtNextAnchor bool   `json:"atNextAnchor,omitempty"`
	When         string `json:"when,omitempty"`
	Count        int    `json:"count,omitempty"` // items
}

// BlockLimits are optional hard rules a station owner can impose on a block.
//
// Off unless set, and deliberately so: the engine is supposed to produce sane
// variety from its own model, and a limit here is a belt, not the trousers. It
// lives on the block rather than in the engine because "no more than ninety
// minutes of people talking" is a taste, and one this station's owner may not
// share with the next.
type BlockLimits struct {
	MaxUnbroken []CategoryLimit `json:"maxUnbroken,omitempty"`
	// MinUnbroken is the other half of the same idea, and the station sounds
	// wrong without it: having started on a category, keep going for a while.
	// A radio station plays a SET. Re-deciding after every three-minute track
	// gives you a song, an episode, a song, an episode — each choice locally
	// reasonable, the sequence deranged.
	MinUnbroken []CategoryMinRun `json:"minUnbroken,omitempty"`
}

// CategoryMinRun keeps a run of one category going once it has started.
type CategoryMinRun struct {
	Category CategoryID `json:"category"`
	Min      string     `json:"min"`
	// ResetAfter matches the MaxUnbroken meaning: how much other content breaks
	// the run.
	ResetAfter string `json:"resetAfter,omitempty"`
}

// CategoryLimit caps an unbroken run of one category.
type CategoryLimit struct {
	Category CategoryID `json:"category"`
	// Max is how much of this category may run unbroken.
	Max string `json:"max"`
	// ResetAfter is how much OTHER content has to air before the run counts as
	// broken. Without it a single three-minute track between two long items
	// resets the counter and the limit never fires.
	ResetAfter string `json:"resetAfter,omitempty"`
	// MinItem is the floor the derived per-item ceiling can never squeeze
	// below, so a station deep into a run can still air a short item rather
	// than finding nothing eligible and going quiet.
	MinItem string `json:"minItem,omitempty"`
}

// LongFormPolicy is how the station treats anything enormous.
//
// Not a ban and not a probability. Something over the threshold is simply
// rationed: once one airs, that show is out of the running for a while, so
// "sometimes, not often" is a fact about the schedule rather than a hope about
// a random number.
type LongFormPolicy struct {
	// Threshold is what counts as long-form. Default two hours.
	Threshold string `json:"threshold,omitempty"`
	// Rest is how long the SHOW steps back after one airs. Default a week,
	// which on a station like this means roughly one giant a week.
	Rest string `json:"rest,omitempty"`
}

func (p LongFormPolicy) threshold() time.Duration { return durationOr(p.Threshold, 2*time.Hour) }

// rest is how long a show steps back after a giant airs.
//
// "never" means back catalogue giants do not come round on their own at all —
// the only thing that puts one on air is a NEW episode, which is exempt from
// rationing because it is news rather than a rerun. That is a real editorial
// position ("I want Hardcore History when Dan puts one out, and not otherwise")
// and it deserves to be sayable rather than approximated with a big number.
func (p LongFormPolicy) rest() time.Duration {
	if strings.EqualFold(strings.TrimSpace(p.Rest), "never") {
		return neverAgain
	}
	return durationOr(p.Rest, 7*24*time.Hour)
}

// neverAgain is longer than any station will be on the air, and is deliberately
// finite so every "how long since" comparison keeps working unchanged.
const neverAgain = 100 * 365 * 24 * time.Hour

// SeparationPolicy is how far apart the same thing may be repeated.
type SeparationPolicy struct {
	Item    string `json:"item,omitempty"`
	Source  string `json:"source,omitempty"`
	Creator string `json:"creator,omitempty"`
	Family  string `json:"family,omitempty"`
}

// Horizons are how far back the station remembers, for different questions.
type Horizons struct {
	// Balance is the window the category mix is measured over.
	Balance string `json:"balance,omitempty"`
	// Rerun is how far back "when did this station last air this" is asked.
	Rerun string `json:"rerun,omitempty"`
	// Recency is how far back the back catalogue still counts as recent, which
	// is what makes "the episode you missed on Tuesday" beat a 2019 rerun.
	// Default a fortnight.
	Recency string `json:"recency,omitempty"`
}

// SelectionPolicy tunes the final choice.
type SelectionPolicy struct {
	// Epsilon is how close to the top score a candidate has to be to be in the
	// running for the weighted random pick, 0..1. Zero always takes the best
	// scoring candidate, which sounds like a machine.
	Epsilon float64 `json:"epsilon,omitempty"`
	// SearchDepth bounds how many items each source offers per decision.
	SearchDepth int `json:"searchDepth,omitempty"`
	// Weights override the scoring terms. Missing terms use their defaults.
	Weights map[string]float64 `json:"weights,omitempty"`
}

// ---- defaults ----------------------------------------------------------

const (
	defaultSeparationItem    = 8 * time.Hour
	defaultSeparationSource  = 45 * time.Minute
	defaultSeparationCreator = 90 * time.Minute
	defaultSeparationFamily  = 45 * time.Minute
	defaultBalanceHorizon    = 6 * time.Hour
	defaultRerunHorizon      = 30 * 24 * time.Hour
	defaultRecencyHorizon    = 14 * 24 * time.Hour
	defaultEpsilon           = 0.15
	defaultSearchDepth       = 200
)

func (p Plan) separationItem() time.Duration {
	return durationOr(p.Separation.Item, defaultSeparationItem)
}
func (p Plan) separationSource() time.Duration {
	return durationOr(p.Separation.Source, defaultSeparationSource)
}
func (p Plan) separationCreator() time.Duration {
	return durationOr(p.Separation.Creator, defaultSeparationCreator)
}
func (p Plan) separationFamily() time.Duration {
	return durationOr(p.Separation.Family, defaultSeparationFamily)
}
func (p Plan) balanceHorizon() time.Duration {
	return durationOr(p.Horizons.Balance, defaultBalanceHorizon)
}
func (p Plan) rerunHorizon() time.Duration {
	return durationOr(p.Horizons.Rerun, defaultRerunHorizon)
}

// longFormFor is the long-form policy in force while a block is on air.
func (p Plan) longFormFor(block Block) LongFormPolicy {
	if block.LongForm != nil {
		return *block.LongForm
	}
	return p.LongForm
}

// minItem is the floor on what counts as programming. Zero means no floor.
func (p Plan) minItem() time.Duration { return durationOr(p.MinItem, 0) }

func (p Plan) recencyHorizon() time.Duration {
	return durationOr(p.Horizons.Recency, defaultRecencyHorizon)
}
func (p Plan) epsilon() float64 {
	if p.Selection.Epsilon > 0 && p.Selection.Epsilon < 1 {
		return p.Selection.Epsilon
	}
	if p.Selection.Epsilon < 0 {
		return 0
	}
	return defaultEpsilon
}
func (p Plan) searchDepth() int {
	if p.Selection.SearchDepth > 0 {
		return p.Selection.SearchDepth
	}
	return defaultSearchDepth
}

// listeningDay is the plan's default exposure schedule.
func (p Plan) listeningDay() (ListeningDay, bool) {
	if p.ListeningDay == nil {
		return ListeningDay{}, false
	}
	start, err := parseClock(p.ListeningDay.Start)
	if err != nil {
		return ListeningDay{}, false
	}
	end, err := parseClock(p.ListeningDay.End)
	if err != nil {
		return ListeningDay{}, false
	}
	return ListeningDay{StartMinute: start, EndMinute: end}, true
}

// ExposureFor is how much airing something in this block, at this moment,
// counts toward the station's obligation to surface it.
//
// A block that states its own exposure wins outright — that is the general
// model, and the one a station with an unusual shape needs. Everything else
// falls back to the listening day, which is the same rule the engine used to
// have hard-coded, now expressed as a default rather than as a law.
func (p Plan) ExposureFor(block Block, at time.Time, fallback ListeningDay) float64 {
	if block.Exposure != nil {
		value := *block.Exposure
		if value < 0 {
			return 0
		}
		if value > 1 {
			return 1
		}
		return value
	}
	day := fallback
	if planDay, ok := p.listeningDay(); ok {
		day = planDay
	}
	if day.Contains(at) {
		return 1
	}
	return 0
}

// Block returns a block by id.
func (p Plan) Block(id string) (Block, bool) {
	for _, block := range p.Blocks {
		if block.ID == id {
			return block, true
		}
	}
	return Block{}, false
}

// DefaultBlock is where everything falls back to.
func (p Plan) DefaultBlock() Block {
	for _, block := range p.Blocks {
		if block.Default {
			return block
		}
	}
	if len(p.Blocks) > 0 {
		return p.Blocks[len(p.Blocks)-1]
	}
	return Block{ID: "default", Default: true}
}

// Pool returns a pool by id.
func (p Plan) Pool(id string) (Pool, bool) {
	for _, pool := range p.Pools {
		if pool.ID == id {
			return pool, true
		}
	}
	return Pool{}, false
}

// CategoryTargets is the share each category is aiming at, normalised to sum to
// one, with a block's own balance applied on top when it has one.
//
// A category nobody has eligible content for hands its share to the others
// rather than leaving part of the schedule permanently unspendable.
func (p Plan) CategoryTargets(block Block, available map[CategoryID]bool) map[CategoryID]float64 {
	raw := map[CategoryID]float64{}
	for _, category := range p.Categories {
		target := category.Target
		if override, ok := block.Balance[category.ID]; ok {
			target = override
		}
		if target < 0 {
			target = 0
		}
		if len(available) > 0 && !available[category.ID] {
			continue
		}
		raw[category.ID] = target
	}
	total := 0.0
	for _, target := range raw {
		total += target
	}
	if total <= 0 {
		// Every eligible category asked for nothing. Split evenly rather than
		// dividing by zero and programming nothing at all.
		if len(raw) == 0 {
			return raw
		}
		even := 1 / float64(len(raw))
		for id := range raw {
			raw[id] = even
		}
		return raw
	}
	for id, target := range raw {
		raw[id] = target / total
	}
	return raw
}

// CategoryOrder is the categories in a stable, plan-declared order.
func (p Plan) CategoryOrder() []CategoryID {
	out := make([]CategoryID, 0, len(p.Categories))
	for _, category := range p.Categories {
		out = append(out, category.ID)
	}
	return out
}

// ---- validation --------------------------------------------------------

// Validate reports every problem with a plan at once.
//
// The infallibility check is the load-bearing one, borrowed from Liquidsoap's
// source model: a station is only guaranteed never to go silent if the fallback
// chain terminates in something that cannot fail. Here that means exactly one
// default block, reachable from everywhere, with at least one pool.
func (p Plan) Validate() error {
	problems := []string{}
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if len(p.Categories) == 0 {
		add("plan has no categories")
	}
	seenCategory := map[CategoryID]bool{}
	for _, category := range p.Categories {
		id := strings.TrimSpace(string(category.ID))
		if id == "" {
			add("a category has no id")
			continue
		}
		if seenCategory[CategoryID(id)] {
			add("duplicate category %q", id)
		}
		seenCategory[CategoryID(id)] = true
	}

	seenPool := map[string]bool{}
	for _, pool := range p.Pools {
		if strings.TrimSpace(pool.ID) == "" {
			add("a pool has no id")
			continue
		}
		if seenPool[pool.ID] {
			add("duplicate pool %q", pool.ID)
		}
		seenPool[pool.ID] = true
	}

	if len(p.Blocks) == 0 {
		add("plan has no blocks")
	}
	defaults := 0
	seenBlock := map[string]bool{}
	for _, block := range p.Blocks {
		if strings.TrimSpace(block.ID) == "" {
			add("a block has no id")
			continue
		}
		if seenBlock[block.ID] {
			add("duplicate block %q", block.ID)
		}
		seenBlock[block.ID] = true
		if block.Default {
			defaults++
			if block.Enter.At != "" || block.Enter.After != "" || block.Enter.When != "" {
				add("the default block %q must have no entry condition — it is where everything falls back to", block.ID)
			}
			if len(block.Pools) == 0 {
				add("the default block %q has no pools, so the station has nothing to fall back on", block.ID)
			}
		}
		for _, ref := range block.Pools {
			if !seenPool[ref.Pool] {
				add("block %q references unknown pool %q", block.ID, ref.Pool)
			}
		}
		for category := range block.Balance {
			if !seenCategory[category] {
				add("block %q balances unknown category %q", block.ID, category)
			}
		}
		for _, limit := range block.Limits.MaxUnbroken {
			if !seenCategory[limit.Category] {
				add("block %q limits unknown category %q", block.ID, limit.Category)
			}
			if _, err := parseDuration(limit.Max); err != nil {
				add("block %q limit on %q: %v", block.ID, limit.Category, err)
			}
		}
		for _, run := range block.Limits.MinUnbroken {
			if !seenCategory[run.Category] {
				add("block %q sets a minimum run for unknown category %q", block.ID, run.Category)
			}
			if _, err := parseDuration(run.Min); err != nil {
				add("block %q minimum run for %q: %v", block.ID, run.Category, err)
			}
		}
		if block.Enter.At != "" {
			if _, err := parseClock(block.Enter.At); err != nil {
				add("block %q enter.at: %v", block.ID, err)
			}
		}
		if block.Enter.Days != "" {
			if _, err := parseWeekdays(block.Enter.Days); err != nil {
				add("block %q enter.days: %v", block.ID, err)
			}
		}
		if block.Enter.Grace != "" {
			if _, err := parseDuration(block.Enter.Grace); err != nil {
				add("block %q enter.grace: %v", block.ID, err)
			}
		}
		switch block.Enter.Start {
		case "", StartMakeNext, StartImmediately, StartWaitUpTo:
		default:
			add("block %q has unknown start policy %q", block.ID, block.Enter.Start)
		}
		// An appointment with no end is not an appointment. Without one it runs
		// until the NEXT booked thing, which on a station with a single booked
		// show is the whole day — and from the outside that presents as "it
		// never switches away from the scheduled programme".
		if block.Enter.Hard && block.Exit.At == "" && block.Exit.Duration == "" &&
			!block.Exit.AtNextAnchor && block.Exit.When == "" && block.Exit.Count == 0 {
			add("block %q is booked at %s but never says when it ends — "+
				"give it an end time, a duration, or 'ends at the next booked slot'",
				block.ID, block.Enter.At)
		}
		if block.Enter.MaxPerDay < 0 {
			add("block %q has maxPerDay %d — a cap cannot be negative",
				block.ID, block.Enter.MaxPerDay)
		}
		// The default block is never asked whether it accepts; it is the answer
		// when nothing else claims the hour. A cap on it would be stored,
		// displayed and silently ignored, which is the failure mode this
		// validator exists to prevent.
		if block.Default && block.Enter.MaxPerDay > 0 {
			add("block %q is the default block, so maxPerDay would never apply — "+
				"the default is what runs when nothing else claims the hour", block.ID)
		}
		// A capped block with nothing to trigger it can only ever be reached by
		// falling through to it, and then its cap stops it coming back. That is
		// a block that runs once and vanishes, which is not what anybody means.
		if block.Enter.MaxPerDay > 0 && block.Enter.At == "" &&
			block.Enter.When == "" && block.Enter.After == "" {
			add("block %q is capped at %d a day but never says when it may run — "+
				"give it a time, an 'after', or a condition like 'window < 45m'",
				block.ID, block.Enter.MaxPerDay)
		}
		if block.Exit.At != "" {
			if _, err := parseClock(block.Exit.At); err != nil {
				add("block %q exit.at: %v", block.ID, err)
			}
		}
		if block.Exit.Duration != "" {
			if _, err := parseDuration(block.Exit.Duration); err != nil {
				add("block %q exit.duration: %v", block.ID, err)
			}
		}
		if _, err := ParseCondition(block.Enter.When); err != nil {
			add("block %q enter.when: %v", block.ID, err)
		}
		if _, err := ParseCondition(block.Exit.When); err != nil {
			add("block %q exit.when: %v", block.ID, err)
		}
		if block.Exposure != nil && (*block.Exposure < 0 || *block.Exposure > 1) {
			add("block %q exposure must be between 0 and 1, got %v", block.ID, *block.Exposure)
		}
		for _, step := range block.Pattern {
			switch step.Want {
			case WantFill, WantObligation, WantBreak:
			default:
				add("block %q has an unknown pattern step %q "+
					"(try: fill, obligation, break)", block.ID, step.Want)
			}
			if step.Want == WantBreak && block.Breaks == nil {
				add("block %q has a break in its pattern but no break policy", block.ID)
			}
		}
		if block.Breaks != nil {
			validateBreaks(block, seenPool, seenCategory, add)
		}
	}
	if defaults == 0 {
		add("no default block — the station has nowhere to fall back to")
	}
	if defaults > 1 {
		add("%d default blocks; there can be only one", defaults)
	}

	// References and cycles in the transition graph.
	for _, block := range p.Blocks {
		if block.Enter.After != "" && !seenBlock[block.Enter.After] {
			add("block %q follows unknown block %q", block.ID, block.Enter.After)
		}
		if block.Next != "" && !seenBlock[block.Next] {
			add("block %q hands over to unknown block %q", block.ID, block.Next)
		}
	}
	for _, block := range p.Blocks {
		if cycle := p.followCycle(block.ID); cycle != "" {
			add("blocks hand over in a loop: %s", cycle)
			break
		}
	}
	for _, name := range p.Separation.each() {
		if _, err := parseDuration(name); err != nil {
			add("separation: %v", err)
		}
	}
	if p.Horizons.Balance != "" {
		if _, err := parseDuration(p.Horizons.Balance); err != nil {
			add("horizons.balance: %v", err)
		}
	}
	if p.Horizons.Rerun != "" {
		if _, err := parseDuration(p.Horizons.Rerun); err != nil {
			add("horizons.rerun: %v", err)
		}
	}
	if p.Horizons.Recency != "" {
		if _, err := parseDuration(p.Horizons.Recency); err != nil {
			add("horizons.recency: %v", err)
		}
	}
	if p.MinItem != "" {
		if _, err := parseDuration(p.MinItem); err != nil {
			add("minItem: %v", err)
		}
	}
	// The one that got away. Every duration is read through durationOr, which
	// swallows a parse error and returns the DEFAULT — so an unvalidated field
	// does not fail, it quietly means something else. longForm.rest was set to
	// "21d", parsed as an error, and became the seven-day default: the giant
	// aired three times in three weeks instead of once, and the plan reported
	// itself as valid throughout.
	if p.LongForm.Threshold != "" {
		if _, err := parseDuration(p.LongForm.Threshold); err != nil {
			add("longForm.threshold: %v", err)
		}
	}
	if p.LongForm.Rest != "" && !strings.EqualFold(strings.TrimSpace(p.LongForm.Rest), "never") {
		if _, err := parseDuration(p.LongForm.Rest); err != nil {
			add("longForm.rest: %v (or \"never\")", err)
		}
	}
	if p.UnderrunPool != "" && !seenPool[p.UnderrunPool] {
		add("underrunPool names unknown pool %q", p.UnderrunPool)
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidID, strings.Join(problems, "; "))
}

// validateBreaks checks a block's break policy. A break that can never be
// assembled — no elements, an impossible count range, a pool that does not
// exist — would show up as a station that mysteriously stops separating its
// programming, so it is caught at save time.
func validateBreaks(block Block, pools map[string]bool, categories map[CategoryID]bool, add func(string, ...any)) {
	policy := block.Breaks
	if len(policy.Elements) == 0 {
		add("block %q has a break policy with no elements", block.ID)
	}
	fills := 0
	for _, element := range policy.Elements {
		if !pools[element.Pool] {
			add("block %q break references unknown pool %q", block.ID, element.Pool)
		}
		low, high := element.countRange()
		if high < low {
			add("block %q break element %q has a backwards count range", block.ID, element.Pool)
		}
		if high <= 0 && element.Fill {
			add("block %q break element %q fills but can never contribute an item", block.ID, element.Pool)
		}
		switch element.Position {
		case "", "first", "last":
		default:
			add("block %q break element %q has unknown position %q (try first or last)",
				block.ID, element.Pool, element.Position)
		}
		if element.Fill {
			fills++
		}
	}
	if fills > 1 {
		add("block %q break has %d elastic elements; only one can absorb the remaining time", block.ID, fills)
	}
	for _, category := range policy.Between {
		if !categories[category] {
			add("block %q break separates unknown category %q", block.ID, category)
		}
	}
	if policy.Target.Duration != "" {
		if _, err := parseDuration(policy.Target.Duration); err != nil {
			add("block %q break target duration: %v", block.ID, err)
		}
	}
	if policy.MinGap != "" {
		if _, err := parseDuration(policy.MinGap); err != nil {
			add("block %q break minGap: %v", block.ID, err)
		}
	}
	for _, raw := range policy.Accept.Duration {
		if _, err := parseDuration(raw); err != nil {
			add("block %q break accept duration: %v", block.ID, err)
		}
	}
}

func (s SeparationPolicy) each() []string {
	out := []string{}
	for _, value := range []string{s.Item, s.Source, s.Creator, s.Family} {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

// followCycle walks the `next` chain from a block and reports the loop it finds,
// if any. A loop would make the fallback walk in §BlockAfter run forever.
func (p Plan) followCycle(start string) string {
	seen := map[string]bool{}
	path := []string{}
	current := start
	for current != "" {
		if seen[current] {
			return strings.Join(append(path, current), " → ")
		}
		seen[current] = true
		path = append(path, current)
		block, ok := p.Block(current)
		if !ok {
			return ""
		}
		current = block.Next
	}
	return ""
}

// ---- serialisation -----------------------------------------------------

// ParsePlan reads and validates a plan document.
func ParsePlan(raw []byte) (Plan, error) {
	var plan Plan
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return Plan{}, fmt.Errorf("%w: %v", ErrInvalidID, err)
	}
	if plan.Version == 0 {
		plan.Version = PlanVersion
	}
	if plan.Version > PlanVersion {
		return Plan{}, fmt.Errorf("%w: plan version %d is newer than this server understands (%d)",
			ErrInvalidID, plan.Version, PlanVersion)
	}
	if err := plan.Validate(); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// ---- deriving a plan from an existing channel --------------------------

// DerivePlan builds the plan a channel's existing configuration already
// describes.
//
// This is what makes the whole model reachable without a migration and without
// a flag day: a channel nobody has touched gets a plan that produces the same
// programming it produced yesterday — the same category split, the same booked
// slots, the same limits — and editing that plan is how anything changes. It is
// also the regression net for the rebuild, because "derived plan behaves like
// the old engine" is a thing tests can assert.
func DerivePlan(channel Channel, sources []Source, rules []ScheduleRule, defaultTalkShare float64) Plan {
	talkShare := channel.TalkShare
	if talkShare <= 0 || talkShare >= 1 {
		talkShare = defaultTalkShare
	}
	if talkShare <= 0 || talkShare >= 1 {
		talkShare = DefaultTalkShare
	}

	day := ListeningDay{StartMinute: channel.DayStartMinute, EndMinute: channel.DayEndMinute}.normalized()
	plan := Plan{
		Version: PlanVersion,
		Categories: []CategoryDef{
			{ID: LegacyCategoryTalk, Label: "Talk", Target: talkShare},
			{ID: LegacyCategoryMusic, Label: "Music", Target: 1 - talkShare},
		},
		// The listening day was a column on the channel and a rule in the
		// engine. It is the same window, now stated where the rest of the
		// programming decisions are — and a block can override it.
		ListeningDay: &DaySpec{
			Start: minuteToClock(day.StartMinute),
			End:   minuteToClock(day.EndMinute),
		},
	}

	// Rotation pools, one per category, plus a pool per source that only ever
	// airs on a booked slot. A show gets its own pool because a slot is an
	// instruction to play THAT, not to play something of its kind.
	interstitial := []string{}
	showPools := map[string]string{}
	for _, src := range sources {
		switch {
		case TraitsFor(src).Interstitial:
			interstitial = append(interstitial, src.ID)
		case src.Role == RoleShow:
			poolID := "show-" + src.ID
			plan.Pools = append(plan.Pools, Pool{
				ID:        poolID,
				Label:     firstNonEmpty(src.Label, "Show"),
				SourceIDs: []string{src.ID},
			})
			showPools[src.ID] = poolID
		}
	}
	// Rotation pools are RULES, not lists. A podcast added next week joins the
	// rotation because it is talk, not because somebody remembered to edit a
	// pool — which is the difference between a station that works and one that
	// silently ignores half its library.
	for _, category := range []CategoryID{LegacyCategoryTalk, LegacyCategoryMusic} {
		plan.Pools = append(plan.Pools, Pool{
			ID:    string(category),
			Label: strings.ToUpper(string(category)),
			Match: &PoolMatch{Category: category},
		})
	}
	if len(interstitial) > 0 {
		plan.Pools = append(plan.Pools, Pool{ID: "interstitial", Label: "Interstitial", SourceIDs: interstitial})
	}
	// Music holds the gap in front of a booked show, so the show starts when it
	// says it does rather than whenever the last thing that fitted ran out.
	//
	// Derived here rather than left empty because every channel has the problem
	// — the gap in front of an appointment always closes to less than the
	// shortest thing the station owns — and a setting nobody knows to switch on
	// is a setting that fixes nothing. A channel with no music simply has no
	// pool to nominate, and keeps the old answer.
	for _, src := range sources {
		if src.Enabled && LegacyCategoryOf(src) == LegacyCategoryMusic && src.Role != RoleShow {
			plan.UnderrunPool = string(LegacyCategoryMusic)
			break
		}
	}

	// The rotation, as one always-available block.
	general := Block{
		ID:      "general",
		Label:   "General rotation",
		Default: true,
		Pools: []PoolRef{
			{Pool: string(LegacyCategoryTalk), Weight: 1},
			{Pool: string(LegacyCategoryMusic), Weight: 1},
		},
		Limits: BlockLimits{
			// The talk governor, carried over as what it always was: a station
			// owner's taste about how long people may talk for, expressed as
			// configuration rather than as a constant in the engine.
			MaxUnbroken: []CategoryLimit{{
				Category:   LegacyCategoryTalk,
				Max:        "90m",
				ResetAfter: "15m",
				MinItem:    "20m",
			}},
			// And the music set, which used to be a hard-coded twenty-minute
			// block in the engine. Without it the balance re-decides after
			// every three-minute track and the station alternates song,
			// episode, song.
			MinUnbroken: []CategoryMinRun{{
				Category:   LegacyCategoryMusic,
				Min:        "20m",
				ResetAfter: "1m",
			}},
		},
	}
	// A channel that owns NOTHING but booked shows still has to have something
	// to play between them.
	//
	// Rotation pools deliberately leave booked shows alone, so a station whose
	// entire library is scheduled streams would otherwise derive a default
	// block that can produce nothing at all — silence, from a channel that was
	// working a moment ago. Where there is no rotation inventory, the shows are
	// the rotation; where there is any, they are not.
	if !hasRotationInventory(sources) {
		for _, src := range sources {
			if pool, ok := showPools[src.ID]; ok {
				general.Pools = append(general.Pools, PoolRef{Pool: pool, Weight: 1})
			}
		}
	}
	// A channel with separator inventory used to get one spot every twenty
	// minutes, from a clock in the engine. Same behaviour, now expressed as the
	// break policy it always was — which means it can become a real stopset
	// (ident, spot, two songs) by editing it instead of by editing Go.
	if len(interstitial) > 0 {
		general.Breaks = &BreakPolicy{
			Target:   BreakSize{Items: 1},
			Accept:   BreakRange{Items: []int{1, 1}},
			Elements: []BreakElement{{Pool: "interstitial", Count: []int{1, 1}}},
			MinGap:   "20m",
		}
	}
	plan.Blocks = append(plan.Blocks, general)

	// Every booked slot becomes a hard-anchored block over its own pool. Sorted
	// so a derived plan is byte-stable for a given channel, which matters for
	// tests and for diffing what changed.
	ordered := append([]ScheduleRule(nil), rules...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartMinute != ordered[j].StartMinute {
			return ordered[i].StartMinute < ordered[j].StartMinute
		}
		return ordered[i].ID < ordered[j].ID
	})
	for _, rule := range ordered {
		if !rule.Enabled {
			continue
		}
		poolID, ok := showPools[rule.SourceID]
		if !ok {
			// A slot pointing at a rotation source: give it a pool of its own so
			// the slot means that source and only that source.
			poolID = "slot-" + rule.SourceID
			if _, exists := plan.Pool(poolID); !exists {
				plan.Pools = append(plan.Pools, Pool{
					ID:        poolID,
					Label:     "Slot source",
					SourceIDs: []string{rule.SourceID},
				})
			}
		}
		plan.Blocks = append(plan.Blocks, Block{
			ID:    "slot-" + rule.ID,
			Label: firstNonEmpty(rule.Label, "Booked slot"),
			Enter: BlockEntry{
				At:   minuteToClock(rule.StartMinute),
				Days: weekdayMaskToSpec(rule.WeekdayMask),
				Hard: true,
				// The old engine cut in on the minute via the preemption
				// watchdog. Kept for derived plans so nothing changes silently;
				// a plan the owner has edited can choose makeNext instead.
				Start: StartImmediately,
			},
			Exit:  BlockExit{At: minuteToClock(rule.EndMinute)},
			Pools: []PoolRef{{Pool: poolID, Weight: 1}},
		})
	}
	return plan
}

// AdoptScheduleRules adds a block for every booked slot the plan does not
// already have one for, and returns what it added.
//
// A stored plan is a snapshot of the schedule at the moment somebody pressed
// save. Book a show afterwards and the rule exists, the UI lists it as ENABLED,
// the programme grid draws it — and the scheduler, which reads the PLAN, has no
// block for it, so at the appointed hour nothing claims the time and the
// station falls back to ordinary rotation. No error anywhere. Exactly the trap
// that frozen pool lists were, one level up.
//
// So booked slots are adopted the same way rotation pools became rules: the
// plan says what the station IS, and the schedule says what is booked. Deleting
// a slot from the plan does not disable it — deleting the RULE does, which is
// the switch the UI actually presents.
func (p Plan) AdoptScheduleRules(rules []ScheduleRule, sources []Source) (Plan, []string) {
	existing := map[string]bool{}
	for _, block := range p.Blocks {
		existing[block.ID] = true
	}
	showPools := map[string]string{}
	for _, pool := range p.Pools {
		if len(pool.SourceIDs) == 1 {
			showPools[pool.SourceIDs[0]] = pool.ID
		}
	}

	ordered := append([]ScheduleRule(nil), rules...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartMinute != ordered[j].StartMinute {
			return ordered[i].StartMinute < ordered[j].StartMinute
		}
		return ordered[i].ID < ordered[j].ID
	})

	adopted := []string{}
	for _, rule := range ordered {
		if !rule.Enabled || existing["slot-"+rule.ID] {
			continue
		}
		poolID, ok := showPools[rule.SourceID]
		if !ok {
			poolID = "slot-" + rule.SourceID
			if _, exists := p.Pool(poolID); !exists {
				p.Pools = append(p.Pools, Pool{
					ID:        poolID,
					Label:     firstNonEmpty(rule.Label, "Slot source"),
					SourceIDs: []string{rule.SourceID},
				})
			}
		}
		p.Blocks = append(p.Blocks, Block{
			ID:    "slot-" + rule.ID,
			Label: firstNonEmpty(rule.Label, "Booked slot"),
			Enter: BlockEntry{
				At:    minuteToClock(rule.StartMinute),
				Days:  weekdayMaskToSpec(rule.WeekdayMask),
				Hard:  true,
				Start: StartImmediately,
			},
			Exit:  BlockExit{At: minuteToClock(rule.EndMinute)},
			Pools: []PoolRef{{Pool: poolID, Weight: 1}},
		})
		adopted = append(adopted, firstNonEmpty(rule.Label, rule.ID))
	}
	return p, adopted
}

// hasRotationInventory reports whether a channel owns anything a rotation pool
// can actually reach — that is, anything that is not a booked show and not
// separator inventory.
func hasRotationInventory(sources []Source) bool {
	for _, src := range sources {
		if !src.Enabled || src.Role == RoleShow || TraitsFor(src).Interstitial {
			continue
		}
		return true
	}
	return false
}

// LegacyCategoryTalk and LegacyCategoryMusic are the two categories a derived
// plan uses.
//
// Named "legacy" because they are a DEFAULT, not a concept the engine knows:
// they exist so a channel that predates plans keeps its talk/music split, and
// nothing outside this file and the derivation above may compare a category to
// either of them.
const (
	LegacyCategoryTalk  CategoryID = "talk"
	LegacyCategoryMusic CategoryID = "music"
)

// LegacyCategoryOf maps a source's role onto a derived plan's categories. Only
// used while deriving; a real plan assigns categories through its pools.
func LegacyCategoryOf(src Source) CategoryID {
	if src.Role == RoleMusic {
		return LegacyCategoryMusic
	}
	return LegacyCategoryTalk
}

// ---- small parsers -----------------------------------------------------

// parseDuration accepts Go duration syntax plus a bare number of minutes, so
// "90m", "1h30m" and "90" all mean the same thing in a config file a person
// types into a browser.
func parseDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if value, err := strconv.Atoi(raw); err == nil {
		return time.Duration(value) * time.Minute, nil
	}
	// Days, because scheduling talks in them. A giant rests for three weeks and
	// a rerun horizon is a month; spelling those "504h" and "720h" is a puzzle,
	// and Go's own parser has no unit for it — so "21d" errored, and every
	// caller reads durations through durationOr, which swallows the error and
	// hands back the DEFAULT. The plan saved cleanly and meant something else.
	if rest, ok := strings.CutSuffix(strings.ToLower(raw), "d"); ok {
		if days, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
			if days < 0 {
				return 0, fmt.Errorf("%q is negative", raw)
			}
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration (try 90m, 1h30m or 21d)", raw)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("%q is negative", raw)
	}
	return parsed, nil
}

func durationOr(raw string, fallback time.Duration) time.Duration {
	parsed, err := parseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// parseClock reads "HH:MM" as a minute of day.
func parseClock(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty time")
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("%q is not a time (try 07:00)", raw)
	}
	hours, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, fmt.Errorf("%q is not a time (try 07:00)", raw)
	}
	minutes, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, fmt.Errorf("%q is not a time (try 07:00)", raw)
	}
	if hours < 0 || hours > 23 || minutes < 0 || minutes > 59 {
		return 0, fmt.Errorf("%q is not a time of day", raw)
	}
	return hours*60 + minutes, nil
}

func minuteToClock(minute int) string {
	if minute < 0 {
		minute = 0
	}
	minute %= 24 * 60
	return fmt.Sprintf("%02d:%02d", minute/60, minute%60)
}

var weekdayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

var weekdayOrder = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// parseWeekdays reads "*", "mon-fri", "sat,sun" or "mon,wed,fri" as a 7-bit
// mask with Sunday in bit 0 — the same encoding schedule rules already use.
func parseWeekdays(raw string) (int, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" || raw == "*" || raw == "all" || raw == "daily" {
		return 127, nil
	}
	mask := 0
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if from, to, ok := strings.Cut(part, "-"); ok {
			start, okFrom := weekdayNames[strings.TrimSpace(from)]
			end, okTo := weekdayNames[strings.TrimSpace(to)]
			if !okFrom || !okTo {
				return 0, fmt.Errorf("%q is not a weekday range", part)
			}
			for day := start; ; day = (day + 1) % 7 {
				mask |= 1 << day
				if day == end {
					break
				}
			}
			continue
		}
		day, ok := weekdayNames[part]
		if !ok {
			return 0, fmt.Errorf("%q is not a weekday", part)
		}
		mask |= 1 << day
	}
	if mask == 0 {
		return 0, fmt.Errorf("%q selects no days", raw)
	}
	return mask, nil
}

// weekdayMaskToSpec is the inverse, for deriving a plan from stored rules.
func weekdayMaskToSpec(mask int) string {
	if mask <= 0 || mask&127 == 127 {
		return "*"
	}
	days := []string{}
	for day, name := range weekdayOrder {
		if mask&(1<<day) != 0 {
			days = append(days, name)
		}
	}
	return strings.Join(days, ",")
}
