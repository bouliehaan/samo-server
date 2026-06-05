package chapteraudio

import (
	"math"
	"sort"
)

type clusterResult struct {
	// SplitSeconds is the chosen gap-duration threshold; gaps at or above it are
	// chapter breaks. Derived from the file's own pause distribution.
	SplitSeconds float64
	// Separation (0..1) is how decisively the chosen level stands out from the
	// shorter pauses (size of the empty band, log scale).
	Separation float64
	// CountMatched is true when a metadata chapter count was supplied AND a real
	// natural break in the audio produced a chapter count close to it — i.e. the
	// audio and the metadata AGREE on how many chapters there are.
	CountMatched bool
	ChapterGaps  []Gap
	Confidence   float64
}

// clusterChapterGaps decides which silences are real chapter breaks. The pause
// durations in a book are multi-modal — word, sentence, paragraph, chapter — so
// there are several "natural breaks" in the sorted (log) durations. Rather than
// blindly taking the single biggest break, we enumerate the real breaks and,
// when we know roughly how many chapters to expect (from embedded/Audnexus
// metadata), pick the break whose resulting count best matches that expectation.
// The audio still decides exactly WHERE each boundary sits and can overrule the
// metadata count when no break supports it. With no metadata we fall back to the
// most prominent break.
//
//	metaChapters: expected chapter count (0 = unknown)
//	fileSeams:    boundaries already guaranteed by file splits
func clusterChapterGaps(gaps []Gap, minRatio float64, metaChapters, fileSeams int) clusterResult {
	if len(gaps) == 0 {
		return clusterResult{SplitSeconds: math.Inf(1)}
	}
	if minRatio <= 1 {
		minRatio = 1.6
	}
	durations := make([]float64, len(gaps))
	for i, g := range gaps {
		durations[i] = g.Duration
	}

	haveTarget := metaChapters > 0
	targetGaps := 0
	if haveTarget {
		targetGaps = metaChapters - 1 - fileSeams // internal breaks the audio still needs to find
		if targetGaps < 0 {
			targetGaps = 0
		}
	}

	split, separation, matched := chooseSplit(durations, minRatio, targetGaps, haveTarget)
	res := collectChapterGaps(gaps, split)
	res.SplitSeconds = split
	res.Separation = separation
	res.CountMatched = matched
	res.Confidence = scoreConfidence(durations, res.ChapterGaps, split, separation, matched)
	return res
}

type splitCandidate struct {
	split float64
	jump  float64 // log-ratio size of the break (bigger = cleaner separation)
	count int     // chapter gaps this split yields
}

// chooseSplit enumerates the significant natural breaks in the sorted pause
// durations and selects one. With a target count it prefers the break that lands
// closest to the target (within tolerance); otherwise — or if nothing lands
// close — it takes the most prominent break. Returns +Inf when no break clears
// the minimum ratio (a flat distribution with no real chapter-pause cluster).
func chooseSplit(durations []float64, minRatio float64, targetGaps int, haveTarget bool) (split, separation float64, matched bool) {
	n := len(durations)
	if n == 0 {
		return math.Inf(1), 0, false
	}
	s := append([]float64(nil), durations...)
	sort.Float64s(s)
	if n == 1 {
		return s[0] * 0.999, 0, false
	}

	minJump := math.Log(minRatio)
	var cands []splitCandidate
	for i := 1; i < n; i++ {
		// Chapter breaks are a minority of all pauses; don't let the "long" class
		// swallow more than ~60% of gaps.
		if float64(n-i) > 0.6*float64(n) {
			continue
		}
		jump := math.Log(s[i]) - math.Log(s[i-1])
		if jump < minJump {
			continue
		}
		cands = append(cands, splitCandidate{
			split: math.Sqrt(s[i-1] * s[i]),
			jump:  jump,
			count: n - i,
		})
	}
	if len(cands) == 0 {
		return math.Inf(1), 0, false
	}

	prominent := cands[0]
	for _, c := range cands[1:] {
		if c.jump > prominent.jump {
			prominent = c
		}
	}

	chosen := prominent
	if haveTarget {
		tol := targetGaps / 6
		if tol < 1 {
			tol = 1
		}
		best := -1
		bestDiff := math.MaxInt32
		for idx, c := range cands {
			diff := c.count - targetGaps
			if diff < 0 {
				diff = -diff
			}
			if diff < bestDiff || (diff == bestDiff && best >= 0 && c.jump > cands[best].jump) {
				bestDiff = diff
				best = idx
			}
		}
		if best >= 0 && bestDiff <= tol {
			chosen = cands[best]
			matched = true
		}
	}

	separation = clampF((chosen.jump-math.Log(1.3))/(math.Log(4)-math.Log(1.3)), 0, 1)
	return chosen.split, separation, matched
}

func collectChapterGaps(gaps []Gap, split float64) clusterResult {
	var out []Gap
	for _, g := range gaps {
		if g.Duration >= split {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartSec < out[j].StartSec })
	return clusterResult{ChapterGaps: out}
}

// scoreConfidence blends signals the inspector prints so a human can see WHY a
// book scored as it did: how decisively the chosen pause cluster separates, the
// empty band around the split, how regular the chapter lengths are, and whether
// the audio count agreed with the metadata.
func scoreConfidence(durations []float64, chapterGaps []Gap, split, separation float64, matched bool) float64 {
	if len(chapterGaps) == 0 {
		return 0
	}

	maxIntra := 0.0
	minChapter := math.Inf(1)
	for _, d := range durations {
		if d >= split {
			if d < minChapter {
				minChapter = d
			}
		} else if d > maxIntra {
			maxIntra = d
		}
	}
	bandScore := 0.6
	if maxIntra > 0 && !math.IsInf(minChapter, 1) {
		ratio := minChapter / maxIntra
		bandScore = clampF((ratio-0.8)/(1.8-0.8), 0, 1)
	}

	conf := 0.5*clampF(separation, 0, 1) + 0.5*bandScore

	if len(chapterGaps) >= 2 {
		lengths := make([]float64, 0, len(chapterGaps)-1)
		for i := 1; i < len(chapterGaps); i++ {
			lengths = append(lengths, chapterGaps[i].MidSec()-chapterGaps[i-1].MidSec())
		}
		cv := coefficientOfVariation(lengths)
		reg := clampF(1-(cv-0.3)/0.9, 0, 1)
		conf *= 0.7 + 0.3*reg
	} else {
		conf *= 0.6
	}

	if matched {
		// Audio and metadata independently agree on the count — strong corroboration.
		conf = clampF(conf+0.15, 0, 1)
	}
	return clampF(conf, 0, 1)
}

func coefficientOfVariation(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))
	if mean == 0 {
		return 0
	}
	var varSum float64
	for _, v := range values {
		d := v - mean
		varSum += d * d
	}
	return math.Sqrt(varSum/float64(len(values))) / mean
}
