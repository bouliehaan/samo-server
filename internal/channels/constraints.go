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

	longFormThreshold time.Duration
	longFormRest      time.Duration

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
				return sinceOK(env.lastByCreator[c.Creator], env.now, env.separationCreator, c.Creator)
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
				return sinceOK(env.lastBySource[c.SourceID], env.now, env.separationSource, "this source")
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
			// Rationing the enormous. Once a giant airs, that show steps back
			// for a week, so one turns up occasionally rather than filling the
			// afternoon whenever there happens to be room for it.
			//
			// Relaxed late but not last: playing a six-hour episode two days
			// running is bad radio, playing nothing at all is worse.
			Name:       "longFormRationing",
			RelaxOrder: 2,
			Check: func(c Candidate, env constraintEnv) (bool, string) {
				if env.longFormThreshold <= 0 || c.Duration < env.longFormThreshold {
					return true, ""
				}
				// Something the station OWES is a different question: a new
				// six-hour episode is news, not a rerun of a special.
				if c.Owed {
					return true, ""
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
	typical := typicalDuration(candidates)
	if typical <= 0 {
		return env
	}
	sources := map[string]bool{}
	creators := map[string]bool{}
	families := map[string]bool{}
	items := 0
	for _, candidate := range candidates {
		if candidate.Ref != "" {
			items++
		}
		if candidate.Traits.SharedCreator {
			sources[candidate.SourceID] = true
		}
		if candidate.Creator != "" && candidate.Traits.HasCreator {
			creators[candidate.Creator] = true
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
	env.separationFamily = fit(env.separationFamily, len(families))
	env.separationItem = fit(env.separationItem, items)
	return env
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
