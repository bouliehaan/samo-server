package chapteraudio

import (
	"math"
	"sort"
)

// BoundaryError is the signed placement error of one chapter boundary against a
// known-true position: placed - truth, in seconds (positive = placed too late).
type BoundaryError struct {
	Index  int     // 1-based chapter index this boundary opens
	Truth  float64 // the true chapter-start second
	Placed float64 // where the analyzer put it
	Signed float64 // Placed - Truth
}

// AlignmentScore summarises how close a set of placed chapter boundaries came to
// the truth — the single yardstick the alignment work is measured against. It is
// deliberately distribution-aware: a median that looks "Audible-grade" can hide a
// handful of deep chapters that are wildly off (the exact CD-rip failure mode), so
// the worst case and the per-threshold fractions are first-class, not footnotes.
type AlignmentScore struct {
	N          int             // boundaries compared
	MedianAbs  float64         // median |error|
	P95Abs     float64         // 95th-percentile |error|
	WorstAbs   float64         // max |error|
	MeanSigned float64         // mean signed error — a non-zero mean is a systematic bias (e.g. silence-start vs heading-onset)
	Within0_2  float64         // fraction within 0.2s
	Within0_5  float64         // fraction within 0.5s — the "Audible-grade" bar
	Within1_0  float64         // fraction within 1.0s
	Errors     []BoundaryError // per-boundary detail, for drill-down
}

// ScoreBoundaries compares placed chapter-start times to the known-true ones,
// position by position (count is assumed equal and in time order — the hard-target
// path guarantees both). It is the metric shared by the synthetic harness and the
// real-library golden-set CLI, so accuracy is always measured the same way.
func ScoreBoundaries(placed, truth []float64) AlignmentScore {
	n := len(truth)
	if len(placed) < n {
		n = len(placed)
	}
	score := AlignmentScore{N: n}
	if n == 0 {
		return score
	}
	abs := make([]float64, 0, n)
	var sumSigned float64
	var in02, in05, in10 int
	for i := 0; i < n; i++ {
		signed := placed[i] - truth[i]
		a := math.Abs(signed)
		score.Errors = append(score.Errors, BoundaryError{Index: i + 1, Truth: truth[i], Placed: placed[i], Signed: signed})
		abs = append(abs, a)
		sumSigned += signed
		if a <= 0.2 {
			in02++
		}
		if a <= 0.5 {
			in05++
		}
		if a <= 1.0 {
			in10++
		}
		if a > score.WorstAbs {
			score.WorstAbs = a
		}
	}
	sort.Float64s(abs)
	score.MedianAbs = percentile(abs, 0.50)
	score.P95Abs = percentile(abs, 0.95)
	score.MeanSigned = sumSigned / float64(n)
	score.Within0_2 = float64(in02) / float64(n)
	score.Within0_5 = float64(in05) / float64(n)
	score.Within1_0 = float64(in10) / float64(n)
	return score
}

// percentile returns the p-quantile (p in [0,1]) of an already-sorted slice using
// linear interpolation between ranks. Empty → 0.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	rank := p * float64(n-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
