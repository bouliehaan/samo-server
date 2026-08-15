package channels

import (
	"fmt"
	"sort"
	"time"
)

// Constraints are the rules a candidate may not break.
//
// The shape is borrowed from how professional music schedulers actually work:
// filter the search depth against hard rules first, score what survives, and —
// when NOTHING survives — relax the least important rule and try again rather
// than failing. That last part is the difference between a station that plays
// something slightly imperfect and a station that goes silent, and it is why
// every constraint carries both a reason and a relax order.

// Rejection is a candidate that did not make it, and why.
type Rejection struct {
	Ref    string `json:"ref"`
	Title  string `json:"title"`
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
}

// constraintEnv is everything the hard rules need to know.
type constraintEnv struct {
	now    time.Time
	window time.Duration
	// cutAtBoundary means this pass is filling the gap in front of an
	// appointment and its pick will be faded out on the boundary, so the fit
	// rule has nothing to protect.
	cutAtBoundary bool

	lastByRef     map[string]time.Time
	lastBySource  map[string]time.Time
	lastByShow    map[string]time.Time
	lastByCreator map[string]time.Time
	lastByFamily  map[string]time.Time

	airings     map[string]int
	lastAirings map[string]time.Time
	listened    map[string]bool

	separationItem    time.Duration
	separationSource  time.Duration
	separationCreator time.Duration
	separationFamily  time.Duration
	// separationByCreator loosens the creator window for anyone who makes up a
	// large share of the library — see fitSeparationToLibrary.
	separationByCreator map[string]time.Duration
	// separationTurn is how long one turn of a shuffled source takes, keyed by
	// source. For those, this replaces the configured item window entirely —
	// see turnsForShuffledSources.
	separationTurn map[string]time.Duration
	// separationFitted marks an env whose windows have already been sized to
	// the library, so the decision path can fit once and hand the same numbers
	// to the rules and to scoring instead of each deciding for itself.
	separationFitted bool

	longFormThreshold time.Duration
	longFormRest      time.Duration
	// lastGiantByShow is when each show last put an ENORMOUS item on air, and
	// how long that item ran.
	//
	// The rest is earned by that airing and covers the whole show, and its
	// length is priced by what the giant cost. Keyed on the show for the usual
	// reason: one programme is routinely two sources.
	lastGiantByShow map[string]LongFormAiring

	limits []ResolvedLimit
	// categoriesPresent is which categories the candidate set contains, so a
	// run limit only bites when there is somewhere else to go.
	categoriesPresent map[CategoryID]int

	skips *SkipRegistry
}

// constraint is one hard rule.
type constraint struct {
	Name string
	// RelaxOrder decides what gets given up first when nothing at all
	// qualifies. Higher relaxes first; below zero never relaxes.
	RelaxOrder int
	Check      func(Candidate, constraintEnv) (bool, string)
}

// constraints is the rule set, most-relaxable first.
//
// The order encodes a judgement about what a listener actually notices. Two
// items from the same producer inside an hour is a blemish; the same episode
// twice is embarrassing; overrunning a booked show is a broken promise, so
// window fitting never relaxes at all.
func standardConstraints() []constraint {
	return []constraint{
		{
			Name:       "familySeparation",
			RelaxOrder: 8,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				if c.Family == "" || env.separationFamily <= 0 {
					return true, ""
				}
				return sinceOK(env.lastByFamily[c.Family], env.now, env.separationFamily, c.Family)
			},
		},
		{
			Name:       "creatorSeparation",
			RelaxOrder: 7,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				if c.Creator == "" || !c.Traits.HasCreator || env.separationCreator <= 0 {
					return true, ""
				}
				window := env.separationCreator
				if fitted, ok := env.separationByCreator[c.Creator]; ok {
					window = fitted
				}
				return sinceOK(env.lastByCreator[c.Creator], env.now, window, c.Creator)
			},
		},
		{
			Name:       "sourceSeparation",
			RelaxOrder: 6,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				// Only for sources that are one show. A playlist is a container
				// of many artists, and separating IT would make two songs in a
				// row impossible — which is most of what a radio station does.
				if !c.Traits.SharedCreator || env.separationSource <= 0 {
					return true, ""
				}
				// Keyed on the SHOW, for the same reason the rationing is: one
				// programme routinely arrives as two sources, the episodes on
				// disk and the same show's feed. Resting the source it aired
				// from while its twin stays eligible spaces out nothing at all.
				// ShowOf falls back to the source, so a show that IS one source
				// is unchanged; the later of the two is taken so the rule can
				// only ever get stricter than it was.
				last := env.lastBySource[c.SourceID]
				if byShow := env.lastByShow[c.Show]; c.Show != "" && byShow.After(last) {
					last = byShow
				}
				return sinceOK(last, env.now, env.separationSource, "this show")
			},
		},
		{
			Name:       "itemSeparation",
			RelaxOrder: 5,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				if c.Ref == "" || env.separationItem <= 0 {
					return true, ""
				}
				// Scaled by how much of this the listener has actually had.
				//
				// Separation exists so you do not hear the same thing twice. An
				// airing that reached nobody — overnight, in a block worth no
				// exposure — is not a time you heard it, so holding the episode
				// back for eight hours afterwards is the station enforcing a
				// rule about an event that did not happen. That is precisely how
				// a morning ends up playing the back catalogue while the new
				// episodes it aired to an empty room sit blocked.
				//
				// Continuous rather than a special case: no credit means no
				// separation, half credit means half the window, a full airing
				// means the whole thing.
				window := env.separationItem
				// A shuffled source is separated by its own queue instead. Eight
				// hours is the wrong answer in both directions: on a three-hundred
				// song playlist it lets a song come round with two hundred others
				// still unplayed, and on a twenty-song one it holds nineteen
				// tracks hostage for an afternoon.
				if turn, ok := env.separationTurn[c.SourceID]; ok && turn > 0 {
					window = turn
				}
				if c.Owed {
					window = time.Duration(float64(window) * c.Credit)
					if window <= 0 {
						return true, ""
					}
				}
				return sinceOK(env.lastByRef[c.Ref], env.now, window, "this item")
			},
		},
		{
			// The run itself: this category has had enough for now. Relaxable,
			// because more of the same beats silence.
			Name:       "categoryRunLimit",
			RelaxOrder: 4,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				for _, limit := range env.limits {
					if limit.Category != c.Category {
						continue
					}
					if limit.Exceeded() && len(env.categoriesPresent) > 1 {
						return false, fmt.Sprintf("%s of unbroken %s already (limit %s)",
							round(limit.Run), c.Category, round(limit.Max))
					}
				}
				return true, ""
			},
		},
		{
			// Rationing the enormous, which is two questions with two answers.
			//
			// How OFTEN may a six-hour epic come round? Rarely — the show steps
			// back for the configured rest, so one turns up occasionally rather
			// than filling the afternoon whenever there is room for it.
			//
			// How soon may that SHOW be heard again at all, once it has taken
			// the whole afternoon? Not for a while, and the whole show is
			// covered, because nobody hears an episode length — they hear the
			// show. That one is priced by what the giant cost rather than by the
			// rationing period, or a feed with a single atypically long episode
			// would disappear for three weeks on the strength of one outlier.
			//
			// Relaxed late but not last: playing a six-hour episode two days
			// running is bad radio, playing nothing at all is worse.
			Name:       "longFormRationing",
			RelaxOrder: 2,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				if env.longFormThreshold <= 0 {
					return true, ""
				}
				giant := c.Duration >= env.longFormThreshold
				if !giant {
					// The rest a giant earns covers the whole show, not just its
					// other giants.
					//
					// Gating on the length of the episode in hand reads as
					// "ration the enormous" and behaves as "ration nothing", for
					// the simple reason that a show which publishes six-hour
					// epics also publishes the odd half-hour piece. Every giant
					// in the feed is refused with "this show aired two days ago";
					// the short one is not a giant, so the rest it is sitting
					// inside is never even consulted, and the show walks straight
					// back on air the same afternoon. Nobody hears an episode
					// length. They hear the show again.
					//
					// It is the AIRING that is asked about rather than the show's
					// usual habits: a feed with one three-hour special among five
					// hundred ordinary episodes is not a long-form show, and
					// resting its whole catalogue because of that one outlier
					// would empty the archive it is there to fill.
					last, resting := env.lastGiantByShow[c.Show]
					if !resting || last.EndedAt.IsZero() {
						return true, ""
					}
					if c.Owed {
						return true, ""
					}
					quiet := showQuietAfter(last.Length, env.longFormRest)
					if since := env.now.Sub(last.EndedAt); since < quiet {
						return false, fmt.Sprintf(
							"this show gave you %s of it %s ago, so it is quiet for %s",
							round(last.Length), round(since), round(quiet))
					}
					return true, ""
				}
				// Something the station OWES is a different question: a new
				// six-hour episode is news, not a rerun of a special.
				if c.Owed {
					return true, ""
				}
				// "Never" is not a very long rest, it is a different rule: back
				// catalogue giants do not come round on their own AT ALL, and
				// the only thing that puts one on air is a new episode, which
				// left above. Without this the first airing is still allowed —
				// nothing has aired yet, so there is nothing to rest from — and
				// a station that wanted none gets one.
				if env.longFormRest >= neverAgain {
					return false, fmt.Sprintf("%s long, and this show only airs on a new episode",
						round(c.Duration))
				}
				// Keyed on the SHOW, not the source. The same programme is
				// routinely two sources — the episodes on disk and the feed —
				// and resting one while the other stays eligible is the same as
				// not rationing at all.
				last, ok := env.lastByShow[c.Show]
				if !ok || last.IsZero() {
					return true, ""
				}
				if since := env.now.Sub(last); since < env.longFormRest {
					return false, fmt.Sprintf("%s long, and this show aired %s ago (a giant rests %s)",
						round(c.Duration), round(since), round(env.longFormRest))
				}
				return true, ""
			},
		},
		{
			Name:       "airingCap",
			RelaxOrder: 3,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				if c.Ref == "" {
					return true, ""
				}
				// Airings that REACHED somebody, for anything still owed.
				//
				// A sixty-minute episode gets one airing a day. If the overnight
				// block spends that airing on an empty room, the episode can
				// never go out in the morning — the cap has been consumed by an
				// event that, by the station's own exposure model, did not
				// happen. Credit is the count of airings that actually landed,
				// so that is the number the cap has to read.
				count := env.airings[c.Ref]
				if c.Owed {
					count = int(c.Credit)
				}
				seconds := int(c.Duration / time.Second)
				if mayAirAgain(seconds, count) {
					return true, ""
				}
				return false, fmt.Sprintf("already reached you %d times today", count)
			},
		},
		{
			// Length, which is a different question from the run and gets a
			// different answer. There is no way out of a six-hour episode once
			// it has started except the skip button, so this is the last rule
			// the engine will give up — below even "somebody skipped this" —
			// and when it does, the record says so.
			Name:       "itemFitsRun",
			RelaxOrder: 0,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				if c.Duration <= 0 {
					return true, ""
				}
				for _, limit := range env.limits {
					if limit.Category != c.Category {
						continue
					}
					// Everything is measured against what is LEFT of the run,
					// including things the station owes.
					//
					// Exempting owed items looked like the fix for a real
					// problem — with twenty-seven minutes left, every
					// forty-five-minute owed episode was rejected and the only
					// things that fitted were short back-catalogue ones, so the
					// ceiling quietly selected OLD content over NEW. But
					// exempting them just moves the damage: the run then runs to
					// twice its limit, which is the marathon the limit exists to
					// prevent.
					//
					// Neither answer is right because the question is wrong. If
					// what is owed will not fit the rest of the run, the run
					// should END — play the music set, then the episode goes out
					// whole into a fresh run. That is what
					// categoriesOutOfRun does, and it is why this rule can go
					// back to being a plain statement about length.
					ceiling := limit.Remaining()
					if c.Duration > ceiling {
						return false, fmt.Sprintf("%s long, but only %s of %s is left in this run",
							round(c.Duration), round(ceiling), c.Category)
					}
				}
				return true, ""
			},
		},
		{
			Name:       "alreadyHeard",
			RelaxOrder: 2,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				if env.listened[c.Ref] {
					return false, "somebody here has already listened to this"
				}
				return true, ""
			},
		},
		{
			Name:       "skipped",
			RelaxOrder: 1,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				if env.skips.RefSuppressed(c.Ref) {
					return false, "skipped recently"
				}
				if env.skips.Suppressed(c.SourceID) {
					return false, "this source was skipped away from recently"
				}
				return true, ""
			},
		},
		{
			// Never relaxed. Relaxing it means starting something that cannot
			// finish before a booked show, and the only ways out of that are
			// cutting it off mid-sentence or running the appointment late.
			Name:       "fitsBeforeAnchor",
			RelaxOrder: -1,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				// Unless being cut off is the job. A gap-filler is chosen in
				// full knowledge that the boundary will take it — that is what
				// keeps the boundary where the schedule put it — so measuring it
				// against a gap nothing can fit would refuse the whole pool and
				// hand the time back to the appointment, early.
				if env.cutAtBoundary {
					return true, ""
				}
				if env.window <= 0 || c.Duration <= 0 {
					// A continuous source has no length of its own; it is
					// bounded by the play window imposed on it instead.
					return true, ""
				}
				if c.Duration <= env.window {
					return true, ""
				}
				return false, fmt.Sprintf("%s long, but only %s until the next booked slot",
					round(c.Duration), round(env.window))
			},
		},
	}
}

// quietPerHourOfAir is how much silence an hour of one show buys that show.
//
// A day off per hour on air. Two different questions were being answered with
// one number and it suited neither: how OFTEN a six-hour epic may come round is
// a rationing decision measured in weeks, while how soon you want to hear that
// same show again after it has taken your whole afternoon is a question about
// the last few days. Charging the second at the first's rate makes a show that
// aired one atypically long episode vanish for three weeks.
//
// Proportionate, so nothing has to be configured per show and the answer scales
// with what the listener actually sat through: four hours of Hardcore History
// buys four days, and a podcast whose one long episode ran two and a half hours
// is back within three days.
const quietPerHourOfAir = 24 * time.Hour

// showQuietAfter is how long a whole show steps back once a giant of its has
// been on, never longer than the rationing period the giants themselves serve.
func showQuietAfter(length, ceiling time.Duration) time.Duration {
	if length <= 0 {
		return 0
	}
	quiet := time.Duration(float64(length) / float64(time.Hour) * float64(quietPerHourOfAir))
	if ceiling > 0 && quiet > ceiling {
		return ceiling
	}
	return quiet
}

// sinceOK is every separation rule, which are all the same rule asked about a
// different attribute.
func sinceOK(last, now time.Time, window time.Duration, what string) (bool, string) {
	if last.IsZero() {
		return true, ""
	}
	since := now.Sub(last)
	if since >= window {
		return true, ""
	}
	return false, fmt.Sprintf("%s aired %s ago, needs %s apart", what, round(since), round(window))
}

func round(d time.Duration) string {
	if d >= time.Hour {
		return d.Round(time.Minute).String()
	}
	if d >= time.Minute {
		return d.Round(time.Minute).String()
	}
	return d.Round(time.Second).String()
}

// rotationHeadroom is how much of a library a separation rule may consume.
//
// Not a tuning constant so much as the difference between a rotation and a
// loop: if the rule demands that everything else airs before anything repeats,
// the running order is fully determined and nothing else — balance, rest,
// the weighted pick — can influence it.
const rotationHeadroom = 0.75

// fitSeparationToLibrary shrinks each separation window to what the available
// content can actually satisfy.
//
// A ninety-minute gap between the same artist is a good rule for a library with
// four hundred artists and an impossible one for a library with three: at three
// artists and four-minute tracks, the tightest achievable spacing is about
// eight minutes, so a ninety-minute rule is not a standard, it is a guarantee
// that the rule gets broken on every third song. The station would still play —
// the relaxation ladder sees to that — but every decision would be recorded as
// a compromise, which makes the record useless for spotting real ones.
//
// So the window becomes the smaller of what was asked for and what the library
// can do: (distinct values − 1) × the typical item length. A rich library is
// unaffected, because the arithmetic exceeds the configured window; a thin one
// quietly gets a rule it can keep. Nothing has to be configured, which is the
// point — the station should work with whatever is there.
func fitSeparationToLibrary(env constraintEnv, candidates []Candidate) constraintEnv {
	if env.separationFitted {
		return env
	}
	env.separationFitted = true
	typical := typicalDuration(candidates)
	if typical <= 0 {
		return env
	}
	sources := map[string]bool{}
	creators := map[string]bool{}
	families := map[string]bool{}
	// How much of the library each creator actually IS, which is a different
	// question from how many creators there are.
	byCreator := map[string]int{}
	items := 0
	for _, candidate := range candidates {
		if candidate.Ref != "" {
			items++
		}
		if candidate.Traits.SharedCreator {
			// Counted as shows, because that is what the rule now separates. A
			// show added twice is one thing to space out, not two, and counting
			// it twice would size the window for a library richer than the one
			// actually there.
			key := candidate.Show
			if key == "" {
				key = candidate.SourceID
			}
			sources[key] = true
		}
		if candidate.Creator != "" && candidate.Traits.HasCreator {
			creators[candidate.Creator] = true
			byCreator[candidate.Creator]++
		}
		if candidate.Family != "" {
			families[candidate.Family] = true
		}
	}
	fit := func(configured time.Duration, distinct int) time.Duration {
		if configured <= 0 {
			return configured
		}
		if distinct <= 1 {
			// One value is no separation at all: a single-artist station cannot
			// keep that artist apart from themselves, and pretending otherwise
			// just means relaxing the rule every time.
			return 0
		}
		// (distinct − 1) × typical is the theoretical maximum, and demanding it
		// would mean every other item must air before anything repeats — which
		// is not a rotation, it is a fixed loop with the order forced. The
		// headroom is what leaves the balance, restedness and the weighted pick
		// anything to actually decide.
		achievable := time.Duration(float64(distinct-1) * float64(typical) * rotationHeadroom)
		if achievable < configured {
			return achievable
		}
		return configured
	}
	env.separationSource = fit(env.separationSource, len(sources))
	env.separationCreator = fit(env.separationCreator, len(creators))

	// Then per creator, by how much of the library they ARE.
	//
	// Counting distinct creators says a hundred and fifteen artists can easily
	// be kept ninety minutes apart. It is blind to the shape of the collection:
	// if a third of the playlist is one artist, holding that artist to the same
	// spacing as one with a single track means the station spends its time
	// refusing to play what it was mostly given. A library that is mostly Elvis
	// is a statement of taste, not an accident to be corrected.
	//
	// The gap between two of an artist's OWN records, if the collection were
	// dealt out evenly: share s means s of every slot is theirs, so between two
	// of them sit (1−s)/s other records.
	//
	// This was typical/s, which is the length of a full turn through the
	// library rather than the gap inside it — too long by exactly one of the
	// artist's own tracks. For somebody with a single track in four hundred the
	// difference is nothing. For somebody who IS half the collection it is
	// nearly double, so the formula over-spaced precisely the artists it was
	// written to stop over-spacing, and capped them below their own share of
	// the shelf however much of it they owned.
	//
	// Headroom applies the same way it does everywhere else — the window comes
	// in UNDER the even-spread gap, so the artist can actually reach their
	// share rather than being held exactly at the theoretical minimum for it.
	// Nobody is ever held to LONGER than the configured window; this only ever
	// relaxes.
	if total := len(candidates); total > 0 && env.separationCreator > 0 {
		windows := make(map[string]time.Duration, len(byCreator))
		for creator, count := range byCreator {
			if count <= 0 {
				continue
			}
			share := float64(count) / float64(total)
			if share >= 1 {
				// The whole pool is one artist. There is nothing to separate
				// them from, and a rule that cannot be kept is not a rule.
				windows[creator] = 0
				continue
			}
			natural := time.Duration(float64(typical) * (1 - share) / share * rotationHeadroom)
			// Under one record's length, the honest window is none.
			//
			// Separation is measured from the END of the last airing, so ANY
			// window above zero means at least one other record in between —
			// which caps the artist at p/(1+p) of the hour however much of the
			// shelf is theirs. Half a playlist comes out as a third of it, and
			// no amount of loosening fixes that, because the floor is one whole
			// track and the arithmetic asks for less.
			//
			// So an artist whose natural gap is shorter than a single record is
			// not separated at all. They are too much of the collection to be
			// kept apart from themselves, which is the same answer fit() gives
			// when there is only one artist in the pool — this is that case
			// arriving by degree rather than all at once. Two of theirs back to
			// back is not the rotation failing; it is what a shelf that is half
			// one artist sounds like.
			if natural < typical {
				windows[creator] = 0
				continue
			}
			if natural < env.separationCreator {
				windows[creator] = natural
			}
		}
		if len(windows) > 0 {
			env.separationByCreator = windows
		}
	}
	env.separationFamily = fit(env.separationFamily, len(families))
	env.separationItem = fit(env.separationItem, items)
	env.separationTurn = turnsForShuffledSources(candidates, typical)
	return env
}

// queueTail is how much of a shuffled source stays in hand.
//
// The queue is expressed as a separation window — a track that may not air
// again until its playlist has run cannot come round while others are still
// waiting — which needs no stored cursor, no shuffle seed, and nothing that can
// drift out of step with what actually went to air. The play log already knows
// what has been played; this is the right question to ask it.
//
// But a window set to the WHOLE turn releases tracks one at a time, in the
// order they were played, so exactly one is ever eligible and the second pass
// replays the first pass note for note. That is not a shuffle, it is a loop,
// and it is the same trap rotationHeadroom exists to keep the separation rules
// out of. A strict shuffle bag has the identical problem at the end of every
// cycle — when one card is left, the "random" pick is forced.
//
// So a tail of the playlist stays in hand and gets shuffled back in: you will
// not hear a record again until nearly all the others have been, and which of
// the remaining few comes next is a real choice, every time, on every pass.
const queueTailShare = 0.10

// minQueueChoices is the floor under that tail, so a twenty-track playlist has
// something to choose between rather than becoming the loop by a different
// route.
const minQueueChoices = 3

// turnsForShuffledSources is how long a shuffled source may hold each of its
// items back — its full running time, less the tail kept in hand.
//
// Summed rather than counted, because a playlist is not uniform and the turn is
// how long it actually RUNS. Items of unknown length count as typical, which is
// the best available guess and keeps one unprobed file from shortening the
// queue for everything else.
func turnsForShuffledSources(candidates []Candidate, typical time.Duration) map[string]time.Duration {
	total := map[string]time.Duration{}
	count := map[string]int{}
	for _, candidate := range candidates {
		if !candidate.Traits.Shuffled || candidate.Ref == "" {
			continue
		}
		length := candidate.Duration
		if length <= 0 {
			length = typical
		}
		total[candidate.SourceID] += length
		count[candidate.SourceID]++
	}
	if len(total) == 0 {
		return nil
	}
	turns := make(map[string]time.Duration, len(total))
	for sourceID, runtime := range total {
		items := count[sourceID]
		if items <= minQueueChoices {
			// Fewer records than the tail we would hold back. Nothing here can
			// be kept apart from itself for a turn, so nothing is.
			continue
		}
		tail := int(float64(items) * queueTailShare)
		if tail < minQueueChoices {
			tail = minQueueChoices
		}
		turns[sourceID] = time.Duration(float64(runtime) * float64(items-tail) / float64(items))
	}
	if len(turns) == 0 {
		return nil
	}
	return turns
}

// typicalDuration is the median length of what is on offer, which is the right
// unit for "how long until this could come round again".
func typicalDuration(candidates []Candidate) time.Duration {
	lengths := make([]time.Duration, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Duration > 0 {
			lengths = append(lengths, candidate.Duration)
		}
	}
	if len(lengths) == 0 {
		return 0
	}
	sort.Slice(lengths, func(i, j int) bool { return lengths[i] < lengths[j] })
	return lengths[len(lengths)/2]
}

// applyConstraints filters the candidate set, relaxing the least important rule
// whenever nothing at all survives.
//
// Returns what qualified, why the rest did not, and which rules had to be given
// up — the last of those matters as much as the answer, because a station
// quietly breaking its own separation rules every hour is a station whose rules
// are wrong, and that should be visible rather than inferred.
func applyConstraints(candidates []Candidate, env constraintEnv) ([]Candidate, []Rejection, []string) {
	rules := standardConstraints()
	relaxed := []string{}
	env = fitSeparationToLibrary(env, candidates)

	for {
		survivors, rejections := constrainOnce(rules, candidates, env)
		if len(survivors) > 0 || len(candidates) == 0 {
			return survivors, rejections, relaxed
		}
		next, remaining, found := dropMostRelaxable(rules)
		if !found {
			return survivors, rejections, relaxed
		}
		relaxed = append(relaxed, next)
		rules = remaining
	}
}

// constrainOnce is a single pass with no relaxation.
func constrainOnce(rules []constraint, candidates []Candidate, env constraintEnv) ([]Candidate, []Rejection) {
	survivors := make([]Candidate, 0, len(candidates))
	rejections := make([]Rejection, 0)
	for _, candidate := range candidates {
		ok := true
		for _, rule := range rules {
			passed, reason := rule.Check(candidate, env)
			if passed {
				continue
			}
			rejections = append(rejections, Rejection{
				Ref:    candidate.Ref,
				Title:  candidate.Title,
				Rule:   rule.Name,
				Reason: reason,
			})
			ok = false
			break
		}
		if ok {
			survivors = append(survivors, candidate)
		}
	}
	return survivors, rejections
}

// anyQualify reports whether any of these candidates get through the rules with
// nothing given up.
//
// Used where breaking a rule is a worse answer than doing something else
// entirely — surfacing a new episode is worth a lot, but not worth playing the
// same host twice in a row to achieve.
// It asks the question the same way the selection path does — WITH the
// relaxation ladder — because otherwise the test applied to what the station
// owes is stricter than the test applied to what replaces it.
//
// That asymmetry was a real bug and a nasty one. A single strict pass rejected
// every owed episode the moment its show had been on earlier in the day, so the
// position fell through to ordinary programming; the back catalogue then went
// through applyConstraints, which cheerfully relaxed that very same separation
// rule to let a five-year-old rerun through. New episode: refused for touching
// a rule. Old episode: allowed to bend it. That is precisely backwards, and it
// is exactly what "playing old podcasts over new ones should never happen when
// there are podcasts owed to me" forbids.
//
// fitsBeforeAnchor never relaxes (RelaxOrder below zero), so this still refuses
// to start a four-hour episode ninety minutes before a booked show.
func anyQualify(candidates []Candidate, env constraintEnv) bool {
	survivors, _, _ := applyConstraints(candidates, env)
	return len(survivors) > 0
}

// owedRejections explains why nothing owed could air, for the decision record.
//
// Without this the record said "what is owed could not air cleanly here" and
// stopped, which is the one question a person actually has at that moment. The
// rejections it reports are for the set that DID air, so the interesting ones
// were being thrown away.
func owedRejections(candidates []Candidate, env constraintEnv) []Rejection {
	_, rejections, _ := applyConstraints(candidates, env)
	return rejections
}

// dropMostRelaxable removes the highest relax-order rule still in play.
func dropMostRelaxable(rules []constraint) (string, []constraint, bool) {
	best := -1
	for index, rule := range rules {
		if rule.RelaxOrder < 0 {
			continue
		}
		if best < 0 || rule.RelaxOrder > rules[best].RelaxOrder {
			best = index
		}
	}
	if best < 0 {
		return "", rules, false
	}
	name := rules[best].Name
	out := make([]constraint, 0, len(rules)-1)
	out = append(out, rules[:best]...)
	out = append(out, rules[best+1:]...)
	return name, out, true
}
