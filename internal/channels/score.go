package channels

import (
	"math"
	"math/rand"
	"sort"
	"time"
)

// Scoring is where preference lives, and it is deliberately a sum of small
// named terms rather than a ladder of if-statements.
//
// A ladder has a property that took a long time to see: the lower rungs are
// unreachable whenever a higher one has anything at all. "A fresh episode" is
// never empty, so under a ladder music literally never played. A weighted sum
// has no unreachable rungs — everything competes, the balance shifts as the
// station's own history changes, and every number that went into a decision can
// be printed afterwards, which a ladder's control flow cannot.

// ScoreTerm is one named contribution to a candidate's score.
type ScoreTerm struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Weight float64 `json:"weight"`
}

// Contribution is what this term actually added.
func (t ScoreTerm) Contribution() float64 { return t.Value * t.Weight }

// ScoredCandidate is a candidate with its arithmetic attached.
type ScoredCandidate struct {
	Candidate Candidate
	Terms     []ScoreTerm
	Total     float64
}

// defaultWeights are the station's taste before anybody expresses one.
//
// Freshness is far and away the largest because a subscription that does not
// surface new episodes promptly has failed at its only job. Run continuity is
// next, because a station that re-decides after every three-minute track stops
// sounding like a station. The rest are corrections rather than drives.
var defaultWeights = map[string]float64{
	"freshness":       4.0,
	"runContinuity":   2.5,
	"commitment":      0.15,
	"categoryDeficit": 1.0,
	"windowFit":       0.8,
	"sourceDeficit":   0.6,
	"restedness":      0.4,
	// Enough to sort the back catalogue by age without ever competing with the
	// obligation queue, which decides what is NEW. This is the difference
	// between "a rerun" and "the episode you missed on Tuesday".
	"recency":    0.9,
	"poolWeight": 0.3,
}

type resolvedMinRun struct {
	Category CategoryID
	Min      time.Duration
	Run      time.Duration
}

// scoreEnv is everything preference is computed from.
type scoreEnv struct {
	now            time.Time
	window         time.Duration
	targetDuration time.Duration

	targets     map[CategoryID]float64
	sourceShare map[string]float64
	airtime     AirtimeWindow

	lastByRef     map[string]time.Time
	lastBySource  map[string]time.Time
	lastByCreator map[string]time.Time

	separationItem    time.Duration
	separationSource  time.Duration
	separationCreator time.Duration

	lastCategory CategoryID
	minRuns      []resolvedMinRun

	// maxUrgency is the most urgent obligation in play, so freshness scores
	// relative to what is actually owed today rather than to an absolute that
	// depends on which tiers a station happens to use.
	maxUrgency float64
	// typicalItem is what this station normally plays, measured from its own
	// recent history rather than configured — a talk channel's norm is forty
	// minutes and a music channel's is three, and neither should have to say so.
	typicalItem time.Duration
	// longFormThreshold is what counts as an enormous item, so the commitment
	// cost applies to those and to nothing else.
	longFormThreshold time.Duration
	// recencyHorizon is how far back "recent" reaches for back catalogue.
	recencyHorizon time.Duration
	maxPool        float64
	weights        map[string]float64
}

func (s scoreEnv) weight(name string) float64 {
	if value, ok := s.weights[name]; ok {
		return value
	}
	return defaultWeights[name]
}

// scoreCandidates works out what the station would prefer, and why.
func scoreCandidates(candidates []Candidate, env scoreEnv) []ScoredCandidate {
	out := make([]ScoredCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		terms := []ScoreTerm{
			{Name: "categoryDeficit", Value: env.categoryDeficit(candidate.Category), Weight: env.weight("categoryDeficit")},
			{Name: "sourceDeficit", Value: env.sourceDeficit(candidate.SourceID), Weight: env.weight("sourceDeficit")},
			{Name: "freshness", Value: env.freshness(candidate), Weight: env.weight("freshness")},
			{Name: "restedness", Value: env.restedness(candidate), Weight: env.weight("restedness")},
			{Name: "recency", Value: env.recency(candidate), Weight: env.weight("recency")},
			{Name: "windowFit", Value: env.windowFit(candidate), Weight: env.weight("windowFit")},
			{Name: "commitment", Value: env.commitment(candidate), Weight: env.weight("commitment")},
			{Name: "runContinuity", Value: env.runContinuity(candidate), Weight: env.weight("runContinuity")},
			{Name: "poolWeight", Value: env.poolPreference(candidate), Weight: env.weight("poolWeight")},
		}
		total := 0.0
		kept := make([]ScoreTerm, 0, len(terms))
		for _, term := range terms {
			total += term.Contribution()
			// A term that contributed nothing is noise in the decision record.
			// Keeping only what moved the answer is what makes the record
			// readable at three in the morning.
			if term.Value != 0 {
				kept = append(kept, term)
			}
		}
		out = append(out, ScoredCandidate{Candidate: candidate, Terms: kept, Total: total})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		// A stable tiebreak on the ref keeps a fixed seed reproducible no
		// matter what order the pools were enumerated in.
		return out[i].Candidate.Ref < out[j].Candidate.Ref
	})
	return out
}

// categoryDeficit is how far behind its share a whole category is, as a
// fraction of the window.
//
// The aggregate, and asked FIRST, because the alternative — ranking every
// source against every other — compares numbers that are not comparable. With
// four podcasts and one playlist at 75/25, each podcast targets 18.75% and the
// playlist 25%, so after a long spoken-word block every individual podcast is
// still further behind its own small slice than the playlist is behind its
// larger one. Talk wins every pick while talk as a whole is hours over. That is
// a fifteen-hour marathon assembled one locally-reasonable decision at a time.
func (s scoreEnv) categoryDeficit(category CategoryID) float64 {
	target, ok := s.targets[category]
	if !ok {
		return 0
	}
	if s.airtime.Total <= 0 {
		return target
	}
	actual := float64(s.airtime.ByCategory[category]) / float64(s.airtime.Total)
	return target - actual
}

// sourceDeficit is the same question one level down, inside a category.
func (s scoreEnv) sourceDeficit(sourceID string) float64 {
	target, ok := s.sourceShare[sourceID]
	if !ok {
		return 0
	}
	if s.airtime.Total <= 0 {
		return target
	}
	actual := float64(s.airtime.BySource[sourceID].Aired) / float64(s.airtime.Total)
	return target - actual
}

// freshness is how much the station owes the listener this particular item.
//
// Zero for anything not owed, so back catalogue never competes on newness. For
// anything owed it is the obligation's own urgency, normalised — which is what
// puts an S-tier show from six hours ago above a C-tier one from ten minutes
// ago, and orders equals newest first, without any of that arithmetic living
// here. Ordering the queue and scoring a candidate are the same question asked
// twice, and they must not be able to disagree.
func (s scoreEnv) freshness(candidate Candidate) float64 {
	if !candidate.Owed {
		return 0
	}
	if s.maxUrgency <= 0 {
		return 1
	}
	value := candidate.Urgency / s.maxUrgency
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// recency prefers back catalogue that is merely recent over back catalogue that
// is ancient.
//
// The station's whole model of "new" is the obligation queue, and once an
// episode has been surfaced it leaves that queue and becomes ordinary back
// catalogue — indistinguishable, until now, from something five years old. But
// a listener who was out for two hours has not heard last Tuesday's episode,
// and that is far more likely to be what they want than a 2019 rerun.
//
// Only applies to things NOT owed: anything still owed is decided by the
// obligation queue's own ordering, and adding a second opinion there would let
// a slightly newer C-tier episode nudge ahead of an S-tier one.
//
// Decays over the horizon rather than cutting off, so there is no cliff where
// an episode stops counting as recent overnight.
func (s scoreEnv) recency(candidate Candidate) float64 {
	if candidate.Owed || candidate.Published.IsZero() {
		return 0
	}
	horizon := s.recencyHorizon
	if horizon <= 0 {
		return 0
	}
	age := s.now.Sub(candidate.Published)
	if age <= 0 {
		return 1
	}
	if age >= horizon {
		return 0
	}
	return 1 - float64(age)/float64(horizon)
}

// restedness is how far past its separation floor the least-rested attribute of
// this candidate is.
//
// The minimum rather than the average on purpose: something whose creator was
// on air twelve minutes ago is not rested just because the item itself has not
// aired in a month.
func (s scoreEnv) restedness(candidate Candidate) float64 {
	value := 1.0
	consider := func(last time.Time, window time.Duration) {
		if window <= 0 || last.IsZero() {
			return
		}
		ratio := float64(s.now.Sub(last)) / float64(2*window)
		if ratio > 1 {
			ratio = 1
		}
		if ratio < 0 {
			ratio = 0
		}
		if ratio < value {
			value = ratio
		}
	}
	consider(s.lastByRef[candidate.Ref], s.separationItem)
	if candidate.Traits.SharedCreator {
		consider(s.lastBySource[candidate.SourceID], s.separationSource)
	}
	if candidate.Creator != "" && candidate.Traits.HasCreator {
		consider(s.lastByCreator[candidate.Creator], s.separationCreator)
	}
	return value
}

// windowFit is how close this item is to the length the block ASKED for.
//
// Only when a block states a target — "about twelve minutes of music here".
// There used to be a second branch: with a booked show ahead, prefer whatever
// fills the gap most completely. That reads as sensible and is a trap. Fitting
// is already guaranteed by the constraint that nothing may overrun an
// appointment, so rewarding length on top of it means the longest thing that
// fits is always the best thing — and in a seven-hour afternoon that is a
// six-hour episode, every time. It turned the gap before a booked show into an
// argument for the giant, which is the opposite of what a gap is.
func (s scoreEnv) windowFit(candidate Candidate) float64 {
	if candidate.Duration <= 0 || s.targetDuration <= 0 {
		return 0
	}
	miss := float64(candidate.Duration-s.targetDuration) / float64(s.targetDuration)
	if miss < 0 {
		miss = -miss
	}
	value := 1 - miss
	if value < 0 {
		return 0
	}
	return value
}

// commitment is what a long item costs the rest of the day.
//
// Negative, and scaled by how much bigger this is than what the station
// normally plays. A six-hour episode against a forty-minute norm is nine times
// the commitment, and it should have to be worth nine ordinary items — which
// most of the time it is not. This is what makes normal programming usually
// win, without banning anything.
func (s scoreEnv) commitment(candidate Candidate) float64 {
	// Only the genuinely enormous pays. A ninety-minute podcast on a station
	// full of ninety-minute podcasts is not a commitment, it is Tuesday, and
	// taxing it would quietly bias the whole rotation toward short episodes.
	if s.longFormThreshold <= 0 || candidate.Duration < s.longFormThreshold {
		return 0
	}
	if s.typicalItem <= 0 {
		return 0
	}
	ratio := float64(candidate.Duration) / float64(s.typicalItem)
	if ratio <= 1 {
		return 0
	}
	// Log-shaped: it grows with size but never becomes a wall. A wall is a ban,
	// and the point is that this can happen — occasionally, when the day has
	// room and nothing else is asking to be heard.
	return -math.Log2(ratio)
}

// runContinuity keeps a set going once it has started.
//
// Only while the run is under the minimum the block asked for, and only for the
// category already on air, so it is a nudge to finish what the station started
// rather than a reason to start it.
func (s scoreEnv) runContinuity(candidate Candidate) float64 {
	if s.lastCategory == "" || candidate.Category != s.lastCategory {
		return 0
	}
	for _, run := range s.minRuns {
		if run.Category != candidate.Category || run.Min <= 0 {
			continue
		}
		if run.Run >= run.Min {
			return 0
		}
		remaining := float64(run.Min-run.Run) / float64(run.Min)
		if remaining > 1 {
			remaining = 1
		}
		return remaining
	}
	return 0
}

func (s scoreEnv) poolPreference(candidate Candidate) float64 {
	if s.maxPool <= 0 || candidate.PoolWeight <= 0 {
		return 0
	}
	return candidate.PoolWeight / s.maxPool
}

// ---- the choice --------------------------------------------------------

// chooseCandidate picks from the candidates that are genuinely in contention.
//
// Not the argmax. Always taking the highest score makes a station predictable
// in a way real radio is not: two near-equivalent records should not resolve
// the same way every Tuesday. But the randomness happens strictly AFTER
// constraints and scoring, among candidates that are already all acceptable
// answers — it is a coin toss between good options, never a way to paper over
// a rule that should have existed.
func chooseCandidate(scored []ScoredCandidate, epsilon float64, rng *rand.Rand) (ScoredCandidate, []ScoredCandidate) {
	if len(scored) == 0 {
		return ScoredCandidate{}, nil
	}
	if epsilon <= 0 || rng == nil || len(scored) == 1 {
		return scored[0], scored[:1]
	}
	top := scored[0].Total
	band := epsilon * absFloat(top)
	if band <= 0 {
		band = epsilon
	}
	threshold := top - band

	contenders := make([]ScoredCandidate, 0, len(scored))
	for _, candidate := range scored {
		if candidate.Total >= threshold {
			contenders = append(contenders, candidate)
			continue
		}
		break // sorted descending
	}

	// The obligation queue is an ORDER, not a suggestion. Controlled randomness
	// is for choosing between records that are genuinely interchangeable; if it
	// also reorders what the station owes, then "this show matters more than
	// that one" becomes "this show usually goes first", and a tier stops
	// meaning anything. So when the best candidate is something owed, only
	// equally urgent things may take its place.
	if contenders[0].Candidate.Owed {
		mostUrgent := contenders[0].Candidate.Urgency
		equal := contenders[:0:0]
		for _, candidate := range contenders {
			if candidate.Candidate.Owed && absFloat(candidate.Candidate.Urgency-mostUrgent) < 0.001 {
				equal = append(equal, candidate)
			}
		}
		if len(equal) > 0 {
			contenders = equal
		}
	}
	if len(contenders) == 1 {
		return contenders[0], contenders
	}

	// Weighted by how far above the bottom of the band each one sits, so the
	// best candidate is still the most likely — just not the certain — answer.
	weights := make([]float64, len(contenders))
	total := 0.0
	for index, candidate := range contenders {
		weight := candidate.Total - threshold
		if weight <= 0 {
			weight = 0.0001
		}
		weights[index] = weight
		total += weight
	}
	roll := rng.Float64() * total
	for index, weight := range weights {
		roll -= weight
		if roll <= 0 {
			return contenders[index], contenders
		}
	}
	return contenders[len(contenders)-1], contenders
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
