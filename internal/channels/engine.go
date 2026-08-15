package channels

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Engine is one channel's programming brain.
//
// Everything it needs is an interface or a value, and nothing in here touches a
// database or a clock directly. That is not tidiness for its own sake: it is
// what lets the simulator run seventy-two hours of this exact code against an
// in-memory history in under a second, which is the difference between finding
// a scheduling bug in a test and finding it by listening to the station for
// nine hours.
type Engine struct {
	Plan    Plan
	Channel Channel
	Sources []Source
	History History
	// Obligations is what the station owes the listener. Nil means it owes
	// nothing and never will, which is a legitimate station: a music channel
	// has no subscriptions to keep up with.
	Obligations ObligationStore
	Catalog     CatalogReader
	Cache       EpisodeCacheLookup
	Stations    InternetStationLookup
	Listened    EpisodeProgressLookup
	Skips       *SkipRegistry
	Location    *time.Location
	Rand        *rand.Rand
	Logger      *log.Logger
}

// defaultLivePlayMinutes bounds a continuous source picked from rotation.
//
// A live stream never ends, so nothing about the item itself would ever tell
// the streamer to move on: without this, the first station the rotation picks
// becomes the channel, permanently. An hour is a radio hour — long enough to be
// worth tuning to, short enough that the rest of the rotation exists.
const defaultLivePlayMinutes = 60

func (e *Engine) source(id string) (Source, bool) {
	for _, src := range e.Sources {
		if src.ID == id {
			return src, true
		}
	}
	return Source{}, false
}

func (e *Engine) location() *time.Location {
	if e.Location != nil {
		return e.Location
	}
	return time.UTC
}

// listeningDay is the fallback exposure schedule: the plan's, or the channel
// columns it was derived from.
func (e *Engine) listeningDay() ListeningDay {
	if day, ok := e.Plan.listeningDay(); ok {
		return day
	}
	return ListeningDay{StartMinute: e.Channel.DayStartMinute, EndMinute: e.Channel.DayEndMinute}
}

// interstitialSources is the separator inventory, by id.
func (e *Engine) interstitialSources() map[string]bool {
	out := map[string]bool{}
	for _, src := range e.Sources {
		if TraitsFor(src).Interstitial {
			out[src.ID] = true
		}
	}
	return out
}

func (e *Engine) logf(format string, args ...any) {
	if e.Logger == nil {
		return
	}
	e.Logger.Printf(format, args...)
}

// Decide is the whole pipeline: what kind of programming, then which item.
//
// It returns the item, the full record of how it got there, and the program
// state to persist. The record is not a debug afterthought — "why the hell did
// it play that" has as many answers as the model has terms, and a station whose
// choices cannot be argued with is a station that gets guessed at instead.
func (e *Engine) Decide(ctx context.Context, now time.Time, state ProgramState) (PlaybackItem, Decision, ProgramState, error) {
	loc := e.location()
	now = now.In(loc)

	timeline := BuildTimeline(e.Plan, now, loc)
	tail, err := e.History.Tail(ctx, 24*time.Hour, 200, now)
	if err != nil {
		tail = nil
	}

	env := e.enumerationEnv(ctx, now, loc)

	// What the station owes, before anything else looks at the world: an
	// episode that dropped at 13:37 is owed at 13:37, not tomorrow morning.
	// Noticing is part of every decision rather than a background sweep,
	// because a sweeper that has not run yet is one more way for the station to
	// behave differently from what its tables say.
	queue := e.refreshObligations(ctx, now, env)
	env.owed = queue

	// A new listening day wipes the per-day allowances before any block is
	// asked whether it may run, so a once-a-day block gets its turn back at the
	// start of the day rather than at midnight UTC.
	state = rollListeningDay(state, e.listeningDay(), loc, now)

	cond := ConditionContext{
		Window: timeline.Window(),
		PoolAvailable: func(poolID string) bool {
			return e.PoolHasContent(ctx, poolID, env)
		},
		ObligationsPending: queue.Len(),
		EnteredToday:       state.EnteredToday,
	}

	block := ResolveBlock(e.Plan, timeline, state, cond, now)

	// Programming already decided and not yet played: a break goes out as the
	// unit it was planned as, rather than being re-derived item by item and
	// drifting into something else halfway through.
	if item, played, ok := e.playQueued(ctx, now, timeline, block, tail, env); ok {
		return item, played.decision, played.state, nil
	}

	// A cycle that says "and now a break" gets one, without waiting to be told
	// by the between-programming rule.
	if block.Block.WantAt(block.State.PatternIndex) == WantBreak {
		if item, planned, ok := e.playBreak(ctx, now, timeline, block, tail, env,
			"the cycle calls for a break here"); ok {
			return item, planned.decision, planned.state, nil
		}
		// Nothing to make one from. Fall through to ordinary programming rather
		// than stalling on a position the library cannot fill.
	}

	item, attempt := e.selectIn(ctx, now, timeline, block, tail, env)
	if attempt.ok {
		// A stopset separates things worth separating, so whether one is due is
		// asked once the station knows what it would play next — a break
		// between two music tracks is not a break, it is an interruption.
		//
		// Only for blocks that do not drive breaks from a pattern: a block that
		// has said where its breaks go does not want them inserted as well.
		if len(block.Block.Pattern) == 0 {
			interstitials := e.interstitialSources()
			if due, reason := breakDue(block.Block.Breaks, block.State, tail, item.Category, now, interstitials); due {
				if breakItem, planned, ok := e.playBreak(ctx, now, timeline, block, tail, env,
					reason+", before "+item.Title); ok {
					planned.decision.Candidates = attempt.decision.Candidates
					return breakItem, planned.decision, planned.state, nil
				}
			}
		}
		next := attempt.state
		next.ItemCount++
		next.PatternIndex++
		next.LastWasBreak = false
		return item, attempt.decision, next, nil
	}

	// The block could not fill the space it has left. When the ONLY thing wrong
	// with every candidate was that it would overrun a boundary, that is not a
	// fault — it is a block that has run out of room, and the answer is to move
	// on to whatever comes next rather than to play something that gets cut off.
	//
	// Two shapes of the same situation: a gap in front of an appointment that
	// has closed to less than anything the station owns (bring the appointment
	// forward), and the tail end of an appointment's own hour (release it
	// early). No threshold decides either; the actual candidate set does.
	if attempt.onlyFitFailures {
		// A gap in front of an appointment, with nothing that fits it.
		//
		// The station's answer used to be "start the appointment early", which
		// is fine for a rotation and wrong for anything a listener has arranged
		// their day around: a music hour that begins whenever the last podcast
		// happened to end is not an hour, it is a surprise. Filling the gap
		// keeps the clock honest — the booked block starts when it says it does.
		//
		// Two passes over the same pool, because "fill the gap" has two
		// meanings and only the second one can hold a boundary. The first asks
		// for something that fits the gap whole, which is the better radio when
		// the station happens to own it. The second accepts being cut off: it is
		// what makes the difference between a booked show that starts at 16:00
		// and one that starts at 15:59:05 because the shortest thing on the
		// station is longer than the hole in front of it.
		//
		// The pool depends on which side of the boundary the hole is on.
		// Outside an appointment it is the one the plan nominated for exactly
		// this — filling the tail of a booked hour from another pool would be
		// playing over the show itself. Inside one it is the block's OWN pool:
		// a music hour with ninety seconds it cannot fill wants one more song
		// faded out on the hour, which is what the hour is for.
		if timeline.Active == nil && e.Plan.UnderrunPool != "" {
			filler := block
			filler.Block.Pools = []PoolRef{{Pool: e.Plan.UnderrunPool, Weight: 1}}
			filler.Block.Pattern = nil
			filler.Block.Breaks = nil
			if item, fill := e.selectIn(ctx, now, timeline, filler, tail, env); fill.ok {
				fill.decision.Note = "nothing left fits before " + timeline.nextLabel() +
					", so the gap is filled rather than starting it early"
				next := fill.state
				next.ItemCount++
				return item, fill.decision, next, nil
			}
			if item, fill, ok := e.holdBoundary(ctx, now, timeline, filler, tail, env); ok {
				next := fill.state
				next.ItemCount++
				return item, fill.decision, next, nil
			}
		}
		if handover, ok := e.blockForBoundary(timeline, block, now); ok {
			item, retry := e.selectIn(ctx, now, timeline, handover, tail, env)
			if retry.ok {
				retry.decision.Note = attempt.boundaryNote(timeline)
				retry.decision.Rejected = attempt.decision.Rejected
				next := retry.state
				next.ItemCount++
				next.PatternIndex++
				return item, retry.decision, next, nil
			}
		}
		// The tail of a booked hour that nothing else could fill either.
		//
		// Releasing it to ordinary programming is the better answer and is tried
		// first, above — a news hour with two minutes left wants a song, not two
		// minutes of another news programme. This is for when that came back
		// empty as well, and the choice is down to one more of the block's own
		// content or handing the last of the hour to whatever is booked next,
		// early.
		if timeline.Active != nil {
			held := block
			held.Block.Pattern = nil
			held.Block.Breaks = nil
			if item, fill, ok := e.holdBoundary(ctx, now, timeline, held, tail, env); ok {
				next := fill.state
				next.ItemCount++
				return item, fill.decision, next, nil
			}
		}
		// Nothing left but to let the appointment at the join come forward, and
		// only across a gap too small to have filled.
		if handover, ok := e.bringAppointmentForward(timeline, block, now); ok {
			item, retry := e.selectIn(ctx, now, timeline, handover, tail, env)
			if retry.ok {
				retry.decision.Note = attempt.boundaryNote(timeline)
				retry.decision.Rejected = attempt.decision.Rejected
				next := retry.state
				next.ItemCount++
				next.PatternIndex++
				return item, retry.decision, next, nil
			}
		}
	}
	return PlaybackItem{}, attempt.decision, block.State, errors.New(attempt.decision.Error)
}

// bringAppointmentForward opens the next booked block early, at a join where
// the one on air has run out of room and nothing at all could fill the sliver.
//
// Two booked blocks back to back leave that sliver — the last ninety seconds of
// a music block, where no track fits before the news. The honest answer, once
// releasing it and holding it have both failed, is that the next booked block
// starts a moment early: it was going to start anyway, and the alternative is
// dead air at the same minute every single day.
//
// "A moment" was never checked, and that is the difference between a sliver at
// a join and a landslide: a music hour ending at 18:00 with forty seconds it
// could not fill handed the airwaves to the 18:30 news, which then went out at
// 17:59:20 — half an hour early, every weekday, reported by nothing. An
// appointment may only come forward across a gap too small to have filled.
func (e *Engine) bringAppointmentForward(timeline Timeline, current BlockDecision, now time.Time) (BlockDecision, bool) {
	if timeline.Active == nil || timeline.Next == nil {
		return BlockDecision{}, false
	}
	if timeline.Next.Start.Sub(now) > minBoundaryFill {
		return BlockDecision{}, false
	}
	booked, ok := e.Plan.Block(timeline.Next.BlockID)
	if !ok || booked.ID == current.Block.ID {
		return BlockDecision{}, false
	}
	anchor := *timeline.Next
	return BlockDecision{
		Block:       booked,
		Anchor:      &anchor,
		EnteredAt:   now,
		EntryReason: "the booked slot before it had no room left, so it starts early",
		ExitReason:  "runs until " + anchor.End.Format("15:04"),
		State:       enteringBlock(current.State, booked.ID, now),
		Changed:     true,
	}, true
}

// holdBoundary fills the last of a block's time with something the clock will
// take, rather than letting the boundary move to meet the silence.
//
// The room it is filling is whichever boundary is nearer: the appointment
// coming, or the end of the appointment already on air. Both are moments the
// schedule promised, and neither may be moved to suit the length of a song.
func (e *Engine) holdBoundary(
	ctx context.Context,
	now time.Time,
	timeline Timeline,
	block BlockDecision,
	tail []PlayTailEntry,
	env enumerationContext,
) (PlaybackItem, selection, bool) {
	room := timeline.Window()
	boundary := timeline.nextLabel()
	if timeline.Active != nil {
		room = timeline.Active.End.Sub(now)
		boundary = "the end of " + timeline.Active.Label
	}
	if room < minBoundaryFill {
		return PlaybackItem{}, selection{}, false
	}
	block.CutAtBoundary = true
	item, fill := e.selectIn(ctx, now, timeline, block, tail, env)
	if !fill.ok {
		return PlaybackItem{}, selection{}, false
	}
	fill.decision.Note = fmt.Sprintf(
		"nothing fits the %s before %s, so the time is held and this is faded out on the boundary",
		round(room), boundary)
	return item, fill, true
}

// minBoundaryFill is the smallest gap worth putting something in.
//
// Below it there is no fill that sounds like anything: a five-second stub of a
// song, faded almost as soon as it starts, is a fault the listener can hear,
// while an appointment that opens five seconds early is one nobody can. The
// point of holding the boundary is that a booked show starts when the schedule
// says — and to within a few seconds, it still does.
const minBoundaryFill = 10 * time.Second

// boundaryFade is how long the gap-filler takes to get out of the way.
//
// Long enough to read as a fade rather than a cut, short enough that most of
// the gap is still music. The appointment starts at its own second either way;
// this only decides what the last moments before it sound like.
const boundaryFade = 3 * time.Second

// pendingObligations reads what the station owes WITHOUT noticing anything new.
//
// The read-only half of refreshObligations, for callers that answer questions
// rather than make decisions — a status peek is asked on every page load and
// must not write.
func (e *Engine) pendingObligations(ctx context.Context, now time.Time) ObligationQueue {
	if e.Obligations == nil {
		return ObligationQueue{}
	}
	stored, err := e.Obligations.List(ctx, now)
	if err != nil {
		e.logf("channel %s: could not read obligations: %v", e.Channel.ID, err)
		return ObligationQueue{}
	}
	return NewObligationQueue(stored, now, e.Plan.Freshness)
}

// refreshObligations notices anything newly published and returns what the
// station currently owes, most urgent first.
func (e *Engine) refreshObligations(ctx context.Context, now time.Time, env enumerationContext) ObligationQueue {
	if e.Obligations == nil {
		return ObligationQueue{}
	}
	// Everything the sources can currently offer that has a publication date
	// inside its own freshness window. Enumeration is already paid for by the
	// decision; noticing costs one insert for genuinely new rows and nothing
	// at all for the rest.
	fresh := []Obligation{}
	// What each source is CALLED, gathered while we are enumerating it anyway.
	//
	// SourceLabel is not a stored column — it is re-derived on read from the
	// source row — so a source with no label leaves the owed list identifying
	// episodes by raw id, which is unreadable and was the reason a Stavvy's
	// World episode could not be recognised as one.
	names := map[string]string{}
	for _, src := range e.Sources {
		if !src.Enabled || !TraitsFor(src).SupportsFreshness {
			continue
		}
		window := freshWindowFor(src)
		tier := TierOf(src)
		for _, candidate := range e.enumerateSource(ctx, src, env) {
			if name := firstNonEmpty(src.Label, candidate.Artist); name != "" {
				names[src.ID] = name
			}
			if candidate.Published.IsZero() || candidate.Published.After(now) {
				continue
			}
			expires := candidate.Published.Add(window)
			if !expires.After(now) {
				continue
			}
			fresh = append(fresh, Obligation{
				ChannelID:   e.Channel.ID,
				SourceID:    src.ID,
				SourceLabel: firstNonEmpty(src.Label, candidate.Artist),
				ItemRef:     candidate.Ref,
				Title:       candidate.Title,
				Tier:        tier,
				PublishedAt: candidate.Published,
				ExpiresAt:   expires,
				SettleAt:    e.Plan.Freshness.SurfacingsFor(tier),
				State:       ObligationPending,
			})
		}
	}
	if len(fresh) > 0 {
		if err := e.Obligations.Notice(ctx, fresh, now); err != nil {
			e.logf("channel %s: could not record new obligations: %v", e.Channel.ID, err)
		}
	}
	stored, err := e.Obligations.List(ctx, now)
	if err != nil {
		e.logf("channel %s: could not read obligations: %v", e.Channel.ID, err)
		return ObligationQueue{}
	}
	// Labels live on the source, not in the row, for anything written before the
	// label changed — but only when the source HAS one. Overwriting a good name
	// with an empty string is how the owed list ended up showing source ids.
	for index := range stored {
		label := names[stored[index].SourceID]
		if src, ok := e.source(stored[index].SourceID); ok && strings.TrimSpace(src.Label) != "" {
			label = src.Label
		}
		if label != "" {
			stored[index].SourceLabel = label
		}
	}
	return NewObligationQueue(stored, now, e.Plan.Freshness)
}

// playQueued takes the next already-decided item, re-validating it against the
// world on the way out.
func (e *Engine) playQueued(
	ctx context.Context,
	now time.Time,
	timeline Timeline,
	block BlockDecision,
	tail []PlayTailEntry,
	env enumerationContext,
) (PlaybackItem, selection, bool) {
	state := block.State
	for len(state.Queue) > 0 {
		queued := state.Queue[0]
		state.Queue = state.Queue[1:]

		src, ok := e.source(queued.SourceID)
		if !ok || !src.Enabled {
			continue
		}
		intent := e.buildIntent(block, timeline, tail, env)
		var chosen *Candidate
		for _, candidate := range e.enumerateSource(ctx, src, env) {
			if candidate.Ref == queued.Ref {
				found := candidate
				chosen = &found
				break
			}
		}
		if chosen == nil {
			continue
		}
		// Still has to fit: an appointment may have arrived since the break was
		// planned, and a queued item is not a licence to overrun it.
		if intent.Window > 0 && chosen.Duration > 0 && chosen.Duration > intent.Window {
			continue
		}
		item, err := e.Materialise(ctx, *chosen)
		if err != nil {
			continue
		}
		e.applyDuration(&item, *chosen, intent, timeline, block)
		item.BlockID = intent.Block.ID
		item.Exposure = e.Plan.ExposureFor(block.Block, now, e.listeningDay())

		decision := Decision{At: now, ChannelID: e.Channel.ID, Timezone: zoneName(e.location(), now)}
		decision.applyIntent(intent, timeline)
		decision.Break = &BreakSummary{
			Items:    []string{item.Title},
			Reason:   queued.Reason,
			Position: queued.Position,
			Of:       queued.Of,
		}
		decision.Selected = &SelectedSummary{
			Ref: item.ItemRef, Title: item.Title, SourceID: item.SourceID,
			Category: string(item.Category),
			Reason:   fmt.Sprintf("part %d of %d of a planned break", queued.Position, queued.Of),
		}
		state.ItemCount++
		state.LastWasBreak = true
		return item, selection{ok: true, decision: decision, state: state}, true
	}
	return PlaybackItem{}, selection{}, false
}

// playBreak assembles a separator and plays its first item, queueing the rest.
//
// Whatever was going to play next is deliberately NOT queued behind it: by the
// time the break finishes the world has moved, and re-deciding is both simpler
// and more correct than honouring a plan made several minutes ago.
func (e *Engine) playBreak(
	ctx context.Context,
	now time.Time,
	timeline Timeline,
	block BlockDecision,
	tail []PlayTailEntry,
	env enumerationContext,
	reason string,
) (PlaybackItem, selection, bool) {
	policy := block.Block.Breaks
	if policy == nil {
		return PlaybackItem{}, selection{}, false
	}

	intent := e.buildIntent(block, timeline, tail, env)
	candidates := e.Enumerate(ctx, intent, env)
	scoring := e.scoreEnv(ctx, now, intent, tail)
	constraints := e.constraintEnv(ctx, now, intent, tail, candidates)
	planned := e.planBreak(ctx, policy, env, scoring, constraints, intent.Window)
	if planned.Empty() {
		// Nothing to make a break out of. Carry on with the programming rather
		// than inserting silence where a stopset was supposed to go.
		return PlaybackItem{}, selection{}, false
	}

	state := block.State
	state.Queue = nil
	for index, candidate := range planned.Items[1:] {
		state.Queue = append(state.Queue, QueuedItem{
			SourceID: candidate.SourceID,
			Ref:      candidate.Ref,
			Reason:   reason,
			Position: index + 2,
			Of:       len(planned.Items),
		})
	}

	first := planned.Items[0]
	item, err := e.Materialise(ctx, first)
	if err != nil {
		return PlaybackItem{}, selection{}, false
	}
	e.applyDuration(&item, first, intent, timeline, block)
	item.BlockID = intent.Block.ID
	item.Exposure = intent.Exposure

	decision := Decision{At: now, ChannelID: e.Channel.ID, Timezone: zoneName(e.location(), now)}
	decision.applyIntent(intent, timeline)
	decision.applyOwed(env.owed, now, e.Plan.Freshness)
	titles := make([]string, 0, len(planned.Items))
	for _, candidate := range planned.Items {
		titles = append(titles, candidate.Title)
	}
	decision.Break = &BreakSummary{
		Items:    titles,
		Minutes:  int(planned.Duration.Minutes()),
		TargetM:  int(policy.targetDuration().Minutes()),
		InRange:  planned.InRange,
		Reason:   reason,
		Note:     planned.Note,
		Position: 1,
		Of:       len(planned.Items),
	}
	decision.Selected = &SelectedSummary{
		Ref: item.ItemRef, Title: item.Title, SourceID: item.SourceID,
		Category: string(item.Category),
		Reason:   "break: " + reason,
	}
	state.ItemCount++
	state.PatternIndex++
	state.LastWasBreak = true
	return item, selection{ok: true, decision: decision, state: state}, true
}

// selection is one attempt to fill a slot from one block.
type selection struct {
	ok       bool
	decision Decision
	state    ProgramState
	// onlyFitFailures means every candidate was rejected for not fitting a
	// boundary, and nothing else was wrong.
	onlyFitFailures bool
	window          time.Duration
}

func (s selection) boundaryNote(timeline Timeline) string {
	if timeline.Active != nil {
		return "the booked slot released early — nothing the station owns fits the " +
			round(s.window) + " left of it"
	}
	return "started the booked slot early — nothing the station owns fits in the " +
		round(s.window) + " before it"
}

// selectIn runs the whole selection pipeline for one block: enumerate,
// constrain, score, choose, materialise.
//
// Split out so the boundary fallbacks re-use the real thing rather than a
// second implementation of it — two code paths that pick items is exactly how a
// scheduler grows a rule that only applies on Tuesdays.
func (e *Engine) selectIn(
	ctx context.Context,
	now time.Time,
	timeline Timeline,
	block BlockDecision,
	tail []PlayTailEntry,
	env enumerationContext,
) (PlaybackItem, selection) {
	intent := e.buildIntent(block, timeline, tail, env)
	decision := Decision{At: now, ChannelID: e.Channel.ID, Timezone: zoneName(e.location(), now)}
	decision.applyIntent(intent, timeline)
	decision.applyOwed(env.owed, now, e.Plan.Freshness)
	out := selection{decision: decision, state: block.State, window: intent.Window}

	candidates := e.Enumerate(ctx, intent, env)
	out.decision.Considered = len(candidates)
	if len(candidates) == 0 {
		out.decision.Error = "no pool in this block could produce anything"
		return PlaybackItem{}, out
	}

	// Why nothing owed could air, kept aside because the rejections for the set
	// that actually aired overwrite the record's list further down — and those
	// answer a different question from the one being asked here.
	var owedWhyNot []Rejection

	// A position in a cycle that asks for something owed gets only things that
	// are owed — but falls through to ordinary programming when nothing is,
	// rather than leaving a hole. A fresh-content cycle that has run dry should
	// hand over, and that is the block's exit condition's job, not this one's.
	if intent.Want == WantObligation {
		owed := filterOwed(candidates)
		switch {
		case len(owed) == 0:
			out.decision.Note = "nothing is owed, so this position played ordinary programming"
		case anyQualify(owed, e.constraintEnv(ctx, now, intent, tail, owed)):
			candidates = owed
			out.decision.Want = string(WantObligation)
		default:
			// Nothing owed can air at all — not even with the relaxations the
			// engine would grant ordinary programming. In practice that means
			// every owed episode would overrun the next booked show, which is
			// the one rule that never bends. Ordinary programming now; the
			// obligation is still owed and comes round again shortly.
			//
			// The reasons go into the record: "what is owed could not air" is
			// the exact moment somebody wants to know WHY, and reporting the
			// rejections for the set that DID air answers a different question.
			out.decision.Note = "nothing owed can air before the next booked show, " +
				"so ordinary programming went out instead"
			owedWhyNot = owedRejections(owed, e.constraintEnv(ctx, now, intent, tail, owed))
		}
	}

	// BACK is an explicit instruction about a specific thing, and the ordinary
	// ordering would guarantee it lands somewhere else: what just played is by
	// definition the most recently aired thing there is.
	// BACK means that ITEM, not something else by the same show.
	//
	// Narrowing to the source and re-scoring across it returned a different
	// episode almost every time, which is what made the button feel random.
	// The item is tried first; the source stays as the fallback for when the
	// exact item is no longer on offer at all.
	if wanted := e.Skips.PreferredRef(e.Channel.ID); wanted != "" {
		if narrowed := filterByRef(candidates, wanted); len(narrowed) > 0 {
			candidates = narrowed
			out.decision.Note = "asked to go back to this item"
		}
	}
	if preferred := e.Skips.PreferredSource(e.Channel.ID); preferred != "" && len(candidates) > 1 {
		if narrowed := filterBySource(candidates, preferred); len(narrowed) > 0 {
			candidates = narrowed
			out.decision.Note = "asked to go back to a particular source"
		}
	}

	// Fitted here rather than inside applyConstraints, so the windows the rules
	// enforce are the same objects scoring is handed further down. Two copies of
	// this arithmetic is how the rules came to allow what the scoring forbade.
	cenv := fitSeparationToLibrary(e.constraintEnv(ctx, now, intent, tail, candidates), candidates)
	survivors, rejections, relaxed := applyConstraints(candidates, cenv)
	out.decision.Rejected = capRejections(append(owedWhyNot, rejections...))
	out.decision.Relaxed = relaxed
	if len(relaxed) > 0 {
		e.logf("channel %s: nothing qualified, gave up %s", e.Channel.ID, strings.Join(relaxed, ", "))
	}
	if len(survivors) == 0 {
		out.decision.Error = "every candidate was ruled out"
		out.onlyFitFailures = len(rejections) > 0
		for _, rejection := range rejections {
			if rejection.Rule != "fitsBeforeAnchor" {
				out.onlyFitFailures = false
				break
			}
		}
		return PlaybackItem{}, out
	}

	// Owed beats back catalogue, as PRECEDENCE and not as a score.
	//
	// Freshness was a weighted term competing with everything else, which meant
	// a five-year-old episode could win on the strength of restedness and
	// source deficit while three new ones sat waiting. Playing back catalogue
	// over something the station owes you is not a trade-off to be balanced; it
	// is simply wrong, and no weighting makes it reliably not happen.
	//
	// Applied WITHIN a category, never across one. Which category plays is
	// still the balance's decision, so this cannot turn into the fifteen-hour
	// talk marathon — music is chosen or not chosen exactly as before. It only
	// settles which spoken item goes out once spoken word has won.
	// End the run rather than substituting old short items for what is owed.
	// Before the owed pass, because the owed items themselves are exactly the
	// ones that did not survive.
	if blocked := categoriesOutOfRun(candidates, cenv); len(blocked) > 0 {
		if narrowed := dropCategories(survivors, blocked); len(narrowed) < len(survivors) {
			survivors = narrowed
			out.decision.Note = "what is owed no longer fits what is left of this run, " +
				"so the run ends here rather than filling it with older, shorter items"
		}
	}

	survivors = dropBackCatalogueOfShowsAwaitingTheirNewEpisode(candidates, survivors, &out.decision)
	survivors = preferOwedWithinCategory(survivors, &out.decision)
	survivors = preferDueLongForm(survivors, cenv, &out.decision)
	survivors = preferNoStub(survivors, intent.PlayCeiling, &out.decision)

	scoring := e.scoreEnv(ctx, now, intent, tail)
	scoring.adoptSeparation(cenv)
	out.decision.applyBalance(intent.Targets, scoring.airtime)
	scored := scoreCandidates(survivors, scoring)
	chosen, contenders := chooseCandidate(scored, e.Plan.epsilon(), e.Rand)
	out.decision.Candidates = summariseCandidates(scored, len(contenders))

	// Try the winner, then the next, and so on: a URL that will not resolve is
	// a fact about one item, not a reason to give up on the whole decision.
	for _, candidate := range append([]ScoredCandidate{chosen}, scored...) {
		item, err := e.Materialise(ctx, candidate.Candidate)
		if err != nil {
			if candidate.Candidate.Ref == chosen.Candidate.Ref {
				out.decision.Rejected = append(out.decision.Rejected, Rejection{
					Ref: candidate.Candidate.Ref, Title: candidate.Candidate.Title,
					Rule: "unplayable", Reason: err.Error(),
				})
			}
			continue
		}
		e.applyDuration(&item, candidate.Candidate, intent, timeline, block)
		item.BlockID = intent.Block.ID
		// Stamped at decision time, not looked up when the item ends: the credit
		// an airing earns belongs to the block that was on air when it STARTED,
		// and by the time it finishes the station may be somewhere else.
		item.Exposure = intent.Exposure
		out.decision.Selected = &SelectedSummary{
			Ref:      item.ItemRef,
			Title:    item.Title,
			SourceID: item.SourceID,
			Category: string(item.Category),
			Score:    candidate.Total,
			Terms:    candidate.Terms,
			Reason:   selectionReason(candidate, len(contenders)),
			Owed:     candidate.Candidate.Owed,
		}
		out.ok = true
		return item, out
	}

	out.decision.Error = "nothing that qualified could actually be played"
	return PlaybackItem{}, out
}

// blockForBoundary is which block should take over when the current one has run
// out of room before a boundary.
func (e *Engine) blockForBoundary(timeline Timeline, current BlockDecision, now time.Time) (BlockDecision, bool) {
	// Inside an appointment: release it to whatever it hands over to.
	if timeline.Active != nil {
		released := timeline
		released.Active = nil
		next := followNext(e.Plan, current.Block, ConditionContext{}, now)
		if next.ID == current.Block.ID {
			return BlockDecision{}, false
		}
		return BlockDecision{
			Block:       next,
			EnteredAt:   now,
			EntryReason: "the booked slot had no room left for another item",
			ExitReason:  blockExitDescription(next),
			State:       enteringBlock(current.State, next.ID, now),
			Changed:     true,
		}, true
	}
	// In front of an appointment: bring it forward.
	if timeline.Next == nil {
		return BlockDecision{}, false
	}
	anchor := *timeline.Next
	block, ok := e.Plan.Block(anchor.BlockID)
	if !ok || block.ID == current.Block.ID {
		return BlockDecision{}, false
	}
	return BlockDecision{
		Block:       block,
		Anchor:      &anchor,
		EnteredAt:   now,
		EntryReason: "nothing fitted the gap in front of it, so it starts early",
		ExitReason:  "runs until " + anchor.End.Format("15:04"),
		State:       enteringBlock(current.State, block.ID, now),
		Changed:     true,
	}, true
}

// buildIntent turns "which block" into "what kind of programming".
func (e *Engine) buildIntent(block BlockDecision, timeline Timeline, tail []PlayTailEntry, env enumerationContext) ProgrammingIntent {
	available := map[CategoryID]bool{}
	for _, ref := range block.Block.Pools {
		pool, ok := e.Plan.Pool(ref.Pool)
		if !ok {
			continue
		}
		for _, src := range pool.Resolve(e.Sources) {
			if src.Enabled && !TraitsFor(src).Interstitial {
				available[SourceCategory(src)] = true
			}
		}
	}
	intent := ProgrammingIntent{
		Block:         block.Block,
		BlockLabel:    blockName(block.Block),
		EnteredAt:     block.EnteredAt,
		EntryReason:   block.EntryReason,
		ExitReason:    block.ExitReason,
		Window:        timeline.Window(),
		PlayCeiling:   timeline.Window(),
		CutAtBoundary: block.CutAtBoundary,
		Targets:       e.Plan.CategoryTargets(block.Block, available),
		Pools:         block.Block.Pools,
		Limits:        resolveLimits(block.Block, tail, block.EnteredAt),
		Want:          block.Block.WantAt(block.State.PatternIndex),
		Exposure:      e.Plan.ExposureFor(block.Block, timeline.Now, e.listeningDay()),
	}
	// Filling a gap is the one time the station has an opinion about length for
	// its own sake: the least of the item lost to the boundary is the best of
	// the pool, so the gap itself becomes the target and windowFit ranks by how
	// near each candidate lands to it.
	if intent.CutAtBoundary && intent.Window > 0 {
		intent.TargetDuration = intent.Window
	}
	for _, obligation := range env.owed.Pending {
		if urgency := obligation.Urgency(timeline.Now, e.Plan.Freshness); urgency > intent.MaxUrgency {
			intent.MaxUrgency = urgency
		}
	}
	// A booked block is bounded by its OWN window, and for the item that OPENS
	// it that bound is a ceiling on how long the station stays, not a rule
	// about what may start: filling the slot is the whole point of booking one,
	// so a sixty-minute show goes out even if we joined the hour five minutes
	// late, and gets cut at the boundary.
	//
	// Once something has aired in the slot, the remaining time becomes a real
	// fit constraint again. Otherwise the last two minutes of a news hour start
	// another hour-long episode purely to play two minutes of it — which burns
	// the episode and sounds like a mistake, because it is one.
	if block.Anchor != nil {
		remaining := block.Anchor.End.Sub(timeline.Now)
		if remaining < 0 {
			remaining = 0
		}
		intent.PlayCeiling = remaining
		intent.Window = remaining
		if block.State.ItemCount == 0 {
			intent.Window = 0
		}
	}
	// A block that says how long it wants to run is asking for items that fit
	// what is left of it — that is how "a short music transition" stays short
	// without anybody counting tracks.
	if block.Block.Exit.Duration != "" {
		if duration, err := parseDuration(block.Block.Exit.Duration); err == nil && duration > 0 {
			remaining := block.EnteredAt.Add(duration).Sub(timeline.Now)
			tolerance, _ := parseDuration(block.Block.Exit.Tolerance)
			if remaining > 0 {
				intent.TargetDuration = remaining
				if intent.Window <= 0 || remaining+tolerance < intent.Window {
					intent.Window = remaining + tolerance
					intent.PlayCeiling = intent.Window
				}
			}
		}
	}
	return intent
}

func (e *Engine) enumerationEnv(ctx context.Context, now time.Time, loc *time.Location) enumerationContext {
	day := e.listeningDay()
	heard, _, err := e.History.AiredInListeningDay(ctx, 30*24*time.Hour, day, loc, now)
	if err != nil {
		heard = map[string]int{}
	}
	return enumerationContext{
		now:         now,
		location:    loc,
		day:         day,
		heardInDay:  heard,
		searchDepth: e.Plan.searchDepth(),
	}
}

// constraintEnv gathers the history the hard rules ask about.
func (e *Engine) constraintEnv(
	ctx context.Context,
	now time.Time,
	intent ProgrammingIntent,
	tail []PlayTailEntry,
	candidates []Candidate,
) constraintEnv {
	lastByRef, err := e.History.LastAiredByRef(ctx, e.Plan.rerunHorizon(), now)
	if err != nil {
		lastByRef = map[string]time.Time{}
	}
	// Wide enough to still see the airing every rule needs to see. A giant
	// rests for a week or three; if the history query only looks back as far as
	// the rerun horizon, the last airing has already fallen off the end and the
	// rationing reads "never played" for ever.
	longForm := e.Plan.longFormFor(intent.Block)
	sourceHorizon := e.Plan.rerunHorizon()
	if rest := longForm.rest(); rest > sourceHorizon && rest < neverAgain {
		sourceHorizon = rest
	}
	lastBySource, err := e.History.LastAiredBySource(ctx, sourceHorizon, now)
	if err != nil {
		lastBySource = map[string]time.Time{}
	}
	// When each show last put an enormous item on air, which is what the rest
	// is actually charged for — asked separately from "when did this source
	// last air anything", because those are different questions with different
	// answers and conflating them either rations nothing or rations everything.
	lastGiant, err := e.History.LastLongFormBySource(ctx, longForm.threshold(), sourceHorizon, now)
	if err != nil {
		lastGiant = map[string]LongFormAiring{}
	}
	airings, lastAirings, err := e.History.ItemAirings(ctx, 24*time.Hour, now)
	if err != nil {
		airings, lastAirings = map[string]int{}, map[string]time.Time{}
	}

	present := map[CategoryID]int{}
	for _, candidate := range candidates {
		present[candidate.Category]++
	}

	// End times, not start times: separation is about how much other content
	// has gone by since, and a three-hour episode that started three hours ago
	// finished a moment ago.
	mergedBySource := withEndTimes(lastBySource, tail, func(e PlayTailEntry) string { return e.SourceID })

	return constraintEnv{
		now:               now,
		window:            intent.Window,
		cutAtBoundary:     intent.CutAtBoundary,
		lastByRef:         withEndTimes(lastByRef, tail, func(e PlayTailEntry) string { return e.ItemRef }),
		lastBySource:      mergedBySource,
		lastByShow:        e.lastByShow(mergedBySource),
		lastByCreator:     e.lastByCreator(tail),
		lastByFamily:      e.lastByFamily(tail),
		airings:           airings,
		lastAirings:       lastAirings,
		listened:          e.listenedRefs(ctx, candidates),
		separationItem:    e.Plan.separationItem(),
		separationSource:  e.Plan.separationSource(),
		separationCreator: e.Plan.separationCreator(),
		separationFamily:  e.Plan.separationFamily(),
		longFormThreshold: longForm.threshold(),
		longFormRest:      longForm.rest(),
		lastGiantByShow:   e.giantsByShow(lastGiant),
		limits:            intent.Limits,
		categoriesPresent: present,
		skips:             e.Skips,
	}
}

func (e *Engine) scoreEnv(ctx context.Context, now time.Time, intent ProgrammingIntent, tail []PlayTailEntry) scoreEnv {
	airtime, err := e.History.Airtime(ctx, e.Plan.balanceHorizon(), now)
	if err != nil {
		airtime = AirtimeWindow{BySource: map[string]SourceAirtime{}, ByCategory: map[CategoryID]time.Duration{}}
	}
	// Separator inventory is not programming, so it does not get a share of the
	// format and its airtime does not count against anybody else's.
	interstitials := map[string]bool{}
	for _, src := range e.Sources {
		if TraitsFor(src).Interstitial {
			interstitials[src.ID] = true
		}
	}
	airtime = airtime.ExcludingSources(interstitials)

	lastByRef, err := e.History.LastAiredByRef(ctx, e.Plan.rerunHorizon(), now)
	if err != nil {
		lastByRef = map[string]time.Time{}
	}
	lastBySource, err := e.History.LastAiredBySource(ctx, e.Plan.rerunHorizon(), now)
	if err != nil {
		lastBySource = map[string]time.Time{}
	}

	env := scoreEnv{
		now:               now,
		window:            intent.Window,
		targetDuration:    intent.TargetDuration,
		targets:           intent.Targets,
		sourceShare:       e.sourceShares(intent),
		airtime:           airtime,
		lastByRef:         lastByRef,
		lastBySource:      lastBySource,
		lastByShow:        e.lastByShow(lastBySource),
		lastByCreator:     e.lastByCreator(tail),
		separationItem:    e.Plan.separationItem(),
		separationSource:  e.Plan.separationSource(),
		separationCreator: e.Plan.separationCreator(),
		maxUrgency:        intent.MaxUrgency,
		typicalItem:       typicalAired(tail),
		longFormThreshold: e.Plan.longFormFor(intent.Block).threshold(),
		recencyHorizon:    e.Plan.recencyHorizon(),
		weights:           e.Plan.Selection.Weights,
	}
	if len(tail) > 0 {
		env.lastCategory = tail[0].Category
	}
	for _, run := range intent.Block.Limits.MinUnbroken {
		min, err := parseDuration(run.Min)
		if err != nil || min <= 0 {
			continue
		}
		resetAfter, _ := parseDuration(run.ResetAfter)
		env.minRuns = append(env.minRuns, resolvedMinRun{
			Category: run.Category,
			Min:      min,
			Run:      CategoryRun(tail, run.Category, resetAfter, intent.EnteredAt),
		})
	}
	for _, ref := range intent.Pools {
		if weight := poolWeight(ref); weight > env.maxPool {
			env.maxPool = weight
		}
	}
	return env
}

// sourceShares splits each category's target between the sources that can serve
// it, by weight. Category first, source second — the order is the whole point.
func (e *Engine) sourceShares(intent ProgrammingIntent) map[string]float64 {
	byCategory := map[CategoryID][]Source{}
	seen := map[string]bool{}
	for _, ref := range intent.Pools {
		pool, ok := e.Plan.Pool(ref.Pool)
		if !ok {
			continue
		}
		for _, src := range pool.Resolve(e.Sources) {
			if !src.Enabled || seen[src.ID] || TraitsFor(src).Interstitial {
				continue
			}
			seen[src.ID] = true
			category := SourceCategory(src)
			byCategory[category] = append(byCategory[category], src)
		}
	}
	out := map[string]float64{}
	for category, group := range byCategory {
		total := 0.0
		for _, src := range group {
			total += sourceWeight(src)
		}
		if total <= 0 {
			continue
		}
		for _, src := range group {
			out[src.ID] = intent.Targets[category] * sourceWeight(src) / total
		}
	}
	return out
}

func sourceWeight(src Source) float64 {
	if src.Weight <= 0 {
		return 1
	}
	return float64(src.Weight)
}

// endedAt is when a play-log entry finished.
//
// Separation is measured from the END, not the start, and the difference is not
// academic: a forty-minute episode that started forty minutes ago finished a
// second ago. Measuring from the start makes "keep the same host forty minutes
// apart" satisfiable by playing them back to back, which is the exact opposite
// of what the rule says.
func endedAt(entry PlayTailEntry) time.Time {
	if entry.Aired > 0 {
		return entry.StartedAt.Add(entry.Aired)
	}
	return entry.StartedAt
}

// lastByCreator reads the recent running order for who was last on air.
//
// The item's own attribution wins where it has one — a playlist is one source
// and four hundred artists, so the source cannot answer this — and falls back
// to the source's creator, which is what a show has.
// lastByShow folds per-source airings onto the programme they came from, so a
// show added twice — episodes on disk, plus the RSS feed — rests as one show.
func (e *Engine) lastByShow(lastBySource map[string]time.Time) map[string]time.Time {
	out := map[string]time.Time{}
	for sourceID, at := range lastBySource {
		src, ok := e.source(sourceID)
		if !ok {
			continue
		}
		if show := ShowOf(src); at.After(out[show]) {
			out[show] = at
		}
	}
	return out
}

// giantsByShow folds per-source giant airings onto the programme they came
// from, so a show added twice — episodes on disk, plus the RSS feed — serves
// one rest rather than two.
func (e *Engine) giantsByShow(bySource map[string]LongFormAiring) map[string]LongFormAiring {
	out := map[string]LongFormAiring{}
	for sourceID, airing := range bySource {
		src, ok := e.source(sourceID)
		if !ok {
			continue
		}
		if show := ShowOf(src); airing.EndedAt.After(out[show].EndedAt) {
			out[show] = airing
		}
	}
	return out
}

func (e *Engine) lastByCreator(tail []PlayTailEntry) map[string]time.Time {
	out := map[string]time.Time{}
	for _, entry := range tail {
		creator := strings.TrimSpace(entry.Artist)
		if creator == "" {
			src, ok := e.source(entry.SourceID)
			if !ok || !TraitsFor(src).HasCreator {
				continue
			}
			creator = CreatorOf(src)
		}
		if creator == "" {
			continue
		}
		if ended := endedAt(entry); ended.After(out[creator]) {
			out[creator] = ended
		}
	}
	return out
}

func (e *Engine) lastByFamily(tail []PlayTailEntry) map[string]time.Time {
	out := map[string]time.Time{}
	for _, entry := range tail {
		src, ok := e.source(entry.SourceID)
		if !ok {
			continue
		}
		family := FamilyOf(src)
		if family == "" {
			continue
		}
		if ended := endedAt(entry); ended.After(out[family]) {
			out[family] = ended
		}
	}
	return out
}

// withEndTimes overlays the store's start-time answers with end times from the
// recent tail, for the entries the tail covers.
//
// The store's aggregates are a MAX(started_at) over a long window, which is the
// right answer for "when did this last come round" but the wrong one for
// separation. The tail knows how long each item ran, so anything recent enough
// to matter gets measured properly, and the long tail keeps the cheap answer.
func withEndTimes(byStart map[string]time.Time, tail []PlayTailEntry, key func(PlayTailEntry) string) map[string]time.Time {
	out := make(map[string]time.Time, len(byStart))
	for id, at := range byStart {
		out[id] = at
	}
	for _, entry := range tail {
		id := key(entry)
		if id == "" {
			continue
		}
		if ended := endedAt(entry); ended.After(out[id]) {
			out[id] = ended
		}
	}
	return out
}

// listenedRefs asks, once, which of these episodes somebody here has already
// sat through.
//
// Across every listener rather than per user, because a channel has no user: it
// is a station the house tunes into, and "somebody here already heard this" is
// the right answer to "should it go on air".
func (e *Engine) listenedRefs(ctx context.Context, candidates []Candidate) map[string]bool {
	out := map[string]bool{}
	if e.Listened == nil {
		return out
	}
	ids := make([]string, 0, len(candidates))
	refByID := map[string]string{}
	durationByID := map[string]int{}
	for _, candidate := range candidates {
		if candidate.episode == nil {
			continue
		}
		ids = append(ids, candidate.episode.ID)
		refByID[candidate.episode.ID] = candidate.Ref
		durationByID[candidate.episode.ID] = candidate.episode.DurationSeconds
	}
	if len(ids) == 0 {
		return out
	}
	progress, err := e.Listened.EpisodeProgress(ctx, ids)
	if err != nil {
		return out
	}
	for id, state := range progress {
		if state.listened(durationByID[id]) {
			out[refByID[id]] = true
		}
	}
	return out
}

// applyDuration bounds how long the station will stay on this item.
func (e *Engine) applyDuration(item *PlaybackItem, candidate Candidate, intent ProgrammingIntent, timeline Timeline, block BlockDecision) {
	if block.Anchor != nil {
		item.IsRuleDriven = true
		item.AnchorBlockID = block.Anchor.BlockID
		item.AnchorPolicy = block.Anchor.Policy
		item.MaxDuration = block.Anchor.End.Sub(timeline.Now)
		if item.MaxDuration < 0 {
			item.MaxDuration = 0
		}
		item.Live = candidate.Traits.Continuous
		// Holding the last of a booked hour with one more of its own: the hour
		// ends on the hour, and this goes quietly under rather than being cut
		// off in the middle of a chorus.
		if intent.CutAtBoundary {
			item.FadeOut = boundaryFade
		}
		return
	}
	if timeline.Next != nil {
		item.AnchorPolicy = timeline.Next.Policy
	}
	// Holding the gap in front of an appointment. The cap IS the boundary, and
	// the fade is what makes the cut sound like radio rather than a fault: the
	// item was picked knowing it would not finish.
	if intent.CutAtBoundary && intent.Window > 0 {
		item.MaxDuration = intent.Window
		item.FadeOut = boundaryFade
		item.Live = candidate.Traits.Continuous
		return
	}
	if !candidate.Traits.Continuous {
		// An item whose length NOBODY KNOWS is the hole in forward fitting: the
		// fit rule waves it through because there is nothing to compare, and it
		// then runs straight over the booked show it was supposed to finish
		// before. Feed episodes routinely report no duration, so this is the
		// common case, not the exotic one.
		//
		// It cannot be rejected — that would rule out most podcasts — so it is
		// bounded instead. Cut at the boundary is worse than a clean finish and
		// far better than an appointment that starts twenty minutes late or not
		// at all.
		if item.DurationSeconds <= 0 && intent.Window > 0 {
			item.MaxDuration = intent.Window
		}
		return
	}
	// A continuous source has no length of its own, so the ceiling has to be
	// applied to how long the station STAYS on it. Without this the first
	// station the rotation picks becomes the channel.
	item.Live = true
	limit := time.Duration(intFromConfig(candidate.source.Config, "playMinutes", defaultLivePlayMinutes)) * time.Minute
	if limit <= 0 {
		limit = defaultLivePlayMinutes * time.Minute
	}
	if intent.PlayCeiling > 0 && intent.PlayCeiling < limit {
		limit = intent.PlayCeiling
	}
	for _, limitDef := range intent.Limits {
		if limitDef.Category == candidate.Category {
			if remaining := limitDef.Remaining(); remaining > 0 && remaining < limit {
				limit = remaining
			}
		}
	}
	item.MaxDuration = limit
}

// preferOwedWithinCategory drops back catalogue from any category that has
// something owed still able to air.
func preferOwedWithinCategory(candidates []Candidate, decision *Decision) []Candidate {
	owedIn := map[CategoryID]bool{}
	for _, candidate := range candidates {
		if candidate.Owed {
			owedIn[candidate.Category] = true
		}
	}
	if len(owedIn) == 0 {
		return candidates
	}
	out := make([]Candidate, 0, len(candidates))
	dropped := 0
	for _, candidate := range candidates {
		if owedIn[candidate.Category] && !candidate.Owed {
			dropped++
			continue
		}
		out = append(out, candidate)
	}
	if dropped > 0 && decision != nil {
		categories := make([]string, 0, len(owedIn))
		for category := range owedIn {
			categories = append(categories, string(category))
		}
		sort.Strings(categories)
		decision.Note = fmt.Sprintf(
			"%d back-catalogue items set aside: %s has something owed, and owed goes first",
			dropped, strings.Join(categories, " and "))
	}
	return out
}

// dropBackCatalogueOfShowsAwaitingTheirNewEpisode is the rule that if there is
// no room for a show's new episode, there is no room for that show.
//
// From the real station, 2026-08-11 15:04. Joey Diaz had published seventy-one
// minutes that morning; fifty-five minutes remained before All Things
// Considered, so it was refused for not fitting — correctly, that rule never
// bends. The station then filled the hole with a thirty-three-minute episode of
// THE SAME SHOW from June 2023, because that one was short enough.
//
// Every rule behaved as written and the result is indefensible. "This does not
// fit, so here is an older one of exactly the same thing" is not a compromise a
// person would ever make; if the new Joey Diaz cannot go out, what the listener
// wants is not an old Joey Diaz, it is something else.
//
// Scoped to the SHOW, deliberately. Dropping the whole category would silence
// every spoken word for the rest of the hour because one podcast was too long,
// and dropping nothing is what produced the 2023 rerun. The show is the unit the
// listener actually perceives, and it is the unit that has something pending.
//
// Only when NONE of that show's owed episodes survived: if one did, it is about
// to play and the ordinary owed precedence handles the rest.
func dropBackCatalogueOfShowsAwaitingTheirNewEpisode(
	candidates, survivors []Candidate,
	decision *Decision,
) []Candidate {
	owed := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.Owed && candidate.Show != "" {
			owed[candidate.Show] = true
		}
	}
	if len(owed) == 0 {
		return survivors
	}
	for _, candidate := range survivors {
		if candidate.Owed {
			delete(owed, candidate.Show)
		}
	}
	if len(owed) == 0 {
		return survivors
	}
	out := make([]Candidate, 0, len(survivors))
	dropped := 0
	for _, candidate := range survivors {
		if owed[candidate.Show] && !candidate.Owed {
			dropped++
			continue
		}
		out = append(out, candidate)
	}
	// Never to the point of silence. If a show's back catalogue is all that is
	// left, the relaxation ladder has already given up everything else and the
	// station plays rather than stopping to admire the problem.
	if dropped == 0 || len(out) == 0 {
		return survivors
	}
	if decision != nil {
		decision.Note = strings.TrimSpace(decision.Note + fmt.Sprintf(
			" %d older episodes set aside: the station owes you a new episode of"+
				" that show and could not fit it, so it is not filling the gap with"+
				" the same show's back catalogue", dropped))
	}
	return out
}

// categoriesOutOfRun is every category that owes the listener something which
// no longer fits what is left of its run.
//
// This is the moment a run limit turns into a length filter if nothing catches
// it. Twenty-seven minutes of talk remain, the station owes three
// forty-five-minute episodes, none of them fit — and the only spoken items that
// DO fit are short back-catalogue reruns. Left alone the station plays a
// five-year-old episode instead of a new one, and does it again at every
// decision, precisely because it owes so much.
//
// The station should stop talking instead. End the run, play the music set the
// block already asks for, and the owed episode goes out whole into a fresh run
// a few minutes later. That is what a person would do, and it is the shape
// Jacob described: some music, then the next podcast.
func categoriesOutOfRun(candidates []Candidate, env constraintEnv) map[CategoryID]bool {
	remaining := map[CategoryID]time.Duration{}
	limited := map[CategoryID]bool{}
	for _, limit := range env.limits {
		remaining[limit.Category] = limit.Remaining()
		limited[limit.Category] = true
	}
	if len(limited) == 0 {
		return nil
	}
	owed := map[CategoryID]bool{}
	fits := map[CategoryID]bool{}
	for _, candidate := range candidates {
		if !candidate.Owed || !limited[candidate.Category] {
			continue
		}
		owed[candidate.Category] = true
		// Unmeasured items are not evidence either way; they are capped to the
		// window elsewhere and must not be read as "nothing fits".
		if candidate.Duration <= 0 || candidate.Duration <= remaining[candidate.Category] {
			fits[candidate.Category] = true
		}
	}
	out := map[CategoryID]bool{}
	for category := range owed {
		if !fits[category] {
			out[category] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// dropCategories removes whole categories from a candidate set, but never
// empties it: a station with nowhere else to go keeps talking rather than
// falling silent.
func dropCategories(candidates []Candidate, drop map[CategoryID]bool) []Candidate {
	if len(drop) == 0 {
		return candidates
	}
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !drop[candidate.Category] {
			out = append(out, candidate)
		}
	}
	if len(out) == 0 {
		return candidates
	}
	return out
}

// preferNoStub keeps a bounded block from painting itself into a corner.
//
// Playing greedily inside a booked hour leaves whatever is left over, and
// "whatever is left over" is eventually ninety seconds — shorter than anything
// the station owns, so the last stretch of a block that was supposed to run to
// the minute has nothing that fits. A person filling that hour would not do
// this: near the end they reach for something that LANDS on the boundary.
//
// So a candidate is preferred when what it leaves behind is playable: either it
// runs to the end, or it leaves room for at least one more item. Only a
// preference — if nothing avoids a stub, the station still plays rather than
// stopping to admire the problem, and the boundary handover covers the rest.
func preferNoStub(candidates []Candidate, ceiling time.Duration, decision *Decision) []Candidate {
	if ceiling <= 0 || len(candidates) < 2 {
		return candidates
	}
	shortest := time.Duration(0)
	for _, candidate := range candidates {
		if candidate.Duration <= 0 {
			continue
		}
		if shortest == 0 || candidate.Duration < shortest {
			shortest = candidate.Duration
		}
	}
	if shortest <= 0 {
		return candidates
	}
	clean := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Duration <= 0 {
			clean = append(clean, candidate)
			continue
		}
		switch remainder := ceiling - candidate.Duration; {
		case remainder <= 0, remainder >= shortest:
			clean = append(clean, candidate)
		}
	}
	if len(clean) == 0 || len(clean) == len(candidates) {
		return candidates
	}
	if decision != nil {
		decision.Note = strings.TrimSpace(decision.Note +
			" preferring something that lands on the boundary, so the end of this block is not a stub")
	}
	return clean
}

// preferDueLongForm gives a rested giant the floor, within its own category.
//
// Rationing decides how OFTEN a six-hour episode may come round; on its own it
// cannot make one actually happen. A long item can never win the scoring
// contest — the commitment cost is a permanent penalty and there is no term
// that rewards length — so left to compete it finishes just outside the
// contender band at every single decision, for ever. The policy then reads as
// "at most once a week" and behaves as "never", which is the shape of a ban.
//
// So a giant is not scored against ordinary programming at all. It is out of
// the running entirely while it is resting (the longFormRationing constraint),
// and it takes precedence once it has rested and there is room for it. That
// makes "sometimes, not often" a property of the schedule instead of an
// accident of arithmetic.
//
// Runs AFTER the owed pass, so a new episode always outranks a rested giant,
// and within a category only, so this can never decide talk-versus-music.
func preferDueLongForm(candidates []Candidate, env constraintEnv, decision *Decision) []Candidate {
	if env.longFormThreshold <= 0 {
		return candidates
	}
	dueIn := map[CategoryID]bool{}
	for _, candidate := range candidates {
		if candidate.Duration < env.longFormThreshold {
			continue
		}
		// Anything still here has already passed the rationing constraint, so
		// it has rested; it has also passed the window rules, so it fits.
		dueIn[candidate.Category] = true
	}
	if len(dueIn) == 0 {
		return candidates
	}
	out := make([]Candidate, 0, len(candidates))
	dropped := 0
	for _, candidate := range candidates {
		if dueIn[candidate.Category] && candidate.Duration < env.longFormThreshold {
			dropped++
			continue
		}
		out = append(out, candidate)
	}
	if dropped > 0 && decision != nil {
		decision.Note = strings.TrimSpace(decision.Note + fmt.Sprintf(
			" a long-form show has rested and there is room for it, so it goes now (%d ordinary items set aside)",
			dropped))
	}
	return out
}

// filterByRef narrows a candidate set to one specific item.
func filterByRef(candidates []Candidate, itemRef string) []Candidate {
	out := make([]Candidate, 0, 1)
	for _, candidate := range candidates {
		if candidate.Ref == itemRef {
			out = append(out, candidate)
		}
	}
	return out
}

// filterOwed narrows a candidate set to things the station actually owes.
func filterOwed(candidates []Candidate) []Candidate {
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Owed {
			out = append(out, candidate)
		}
	}
	return out
}

func filterBySource(candidates []Candidate, sourceID string) []Candidate {
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.SourceID == sourceID {
			out = append(out, candidate)
		}
	}
	return out
}

func selectionReason(_ ScoredCandidate, contenders int) string {
	if contenders <= 1 {
		return "highest scoring candidate"
	}
	return "weighted pick among " + strconv.Itoa(contenders) + " candidates within reach of the top score"
}
