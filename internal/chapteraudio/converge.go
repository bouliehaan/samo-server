package chapteraudio

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// gateOffsetsDB is the ladder of silence-gate loosenings the count-driven search
// walks. Each value is added to a file's OWN adaptive silence ceiling (in dB), so
// a higher rung admits quieter-but-not-quite-floor frames as silence and surfaces
// more (and longer) gaps. The search climbs the ladder only as far as it must to
// expose enough boundary candidates to reach the authoritative chapter count —
// when the file's natural floor already finds more than enough, it stops at rung
// 0 and the surplus is trimmed by keeping only the most chapter-like candidates.
//
// We only ever LOOSEN, never tighten: when there are already too many silences,
// selecting the best N is the right move, not raising the bar so high that a
// genuinely quiet chapter break disappears.
var gateOffsetsDB = []float64{0, 2, 4, 6, 8, 10, 12}

// seamDistanceWeight discounts a file seam's distance when assigning candidates
// to expected chapter positions: a seam "counts as closer" than a silence at the
// same distance. A seam is a perfect cut (zero placement error) and publishers
// that split per chapter put seams AT chapter starts — but a seam is NOT proof
// of a chapter (a CD rip splits files every few minutes wherever the disc
// happened to end), so the discount is a thumb on the scale, never a trump: a
// silence right at the expected position still beats a seam minutes away.
const seamDistanceWeight = 0.6

// seamBaseScore ranks a seam in the no-positions fallback (metadata supplied a
// count but no usable timestamps), comparable to gapCandidate's log-duration
// scale: a seam ranks like a ~6 s clean silence.
const seamBaseScore = 1.94

// ladderRung is the set of book-global silences found at one gate offset, pooled
// across every file in the book.
type ladderRung struct {
	gateOffsetDB float64
	gaps         []Gap
}

// loosenGate returns th with its silence ceiling raised by offsetDB, clamped so
// it can never climb above (SpeechDB - gateHeadroomDB): past that we'd be calling
// the narration itself silent. A clamp that would push the ceiling BELOW the
// file's own floor is ignored (the offset just has no effect on that file).
func loosenGate(th thresholds, offsetDB float64) thresholds {
	if offsetDB <= 0 {
		return th
	}
	const gateHeadroomDB = 6.0
	ceil := th.SpeechDB - gateHeadroomDB
	if ceil < th.SilenceDB {
		ceil = th.SilenceDB
	}
	raised := th.SilenceDB + offsetDB
	if raised > ceil {
		raised = ceil
	}
	th.SilenceDB = raised
	return th
}

// chapterCandidate is one potential internal chapter boundary: either a file
// seam (exact split offset, no duration) or a detected silence (boundary placed
// per BoundaryAtSilenceStart).
type chapterCandidate struct {
	Time      float64
	FromFile  bool
	score     float64
	supported bool // scored as a plausible chapter break, not a desperation pick
}

// convergeResult is what the count-driven search settled on.
type convergeResult struct {
	chosen       []chapterCandidate // exactly the internal boundaries to write, time order
	gateOffsetDB float64            // ladder rung the search converged at
	cutoffSec    float64            // shortest chosen silence (0 when all boundaries are seams)
	matched      bool               // found exactly the number of breaks the target demanded
	need, found  int
	note         string
}

// analyzeHardTarget runs the count-driven detector and finishes the report. The
// metadata count (len(meta)) is authoritative: boundary candidates — file seams
// AND detected silences, both scored — are selected until the book has exactly
// that many chapters. File seams are candidates, never certainties: a
// chapter-per-file book converges by keeping all its seams (they sit where the
// metadata expects chapters), while a CD rip whose ~150 track seams outnumber
// the real chapters keeps only the seams (and silences) that look like chapter
// starts. When the audio genuinely cannot produce the demanded count the
// detector keeps what it found, flags CountMatched=false, and recommends review
// so the caller leaves the existing chapters alone rather than inventing
// positions.
func (a *Analyzer) analyzeHardTarget(rep *Report, bookRungs []ladderRung, fileStarts []float64, meta []catalog.AudioChapter) *Report {
	count := len(meta)
	rep.TargetCount = count
	rep.HardTarget = true

	// The expected chapter positions carry the metadata's own timing error. When
	// the metadata's total runtime disagrees with the files (a slightly different
	// cut of the same edition), every deep position is off by up to that delta —
	// so the drift tolerance widens to absorb it rather than punishing every
	// candidate for the edition gap.
	tolerance := a.Params.PositionDriftToleranceSec
	if cov := metaCoverageSeconds(meta); cov > 0 && rep.DurationSec > 0 {
		if delta := math.Abs(cov - rep.DurationSec); delta > tolerance {
			tolerance = delta
		}
	}

	need := count - 1
	expected := expectedInternalStarts(meta)
	res := convergeBoundaries(bookRungs, fileStarts, expected, need, a.Params, rep.DurationSec, tolerance)
	rep.GateOffsetDB = res.gateOffsetDB
	rep.SplitSeconds = res.cutoffSec
	if res.note != "" {
		rep.Notes = append(rep.Notes, res.note)
	}

	var cands []boundaryCand
	for _, c := range res.chosen {
		cands = append(cands, boundaryCand{Time: c.Time, FromFile: c.FromFile})
	}
	// Selection already spaced and edge-trimmed the picks; merge is the shared
	// final safety net (sort + collapse), kept so both paths emit through one door.
	merged := mergeBoundaries(cands, a.Params.MergeWithinSeconds, a.Params.EdgeMarginSeconds, rep.DurationSec)

	exact := len(merged)+1 == count
	a.fillBoundaries(rep, merged, meta, exact)
	rep.CountMatched = exact
	rep.Separation = silenceSeparation(bookRungs[0].gaps, res.cutoffSec)
	rep.Confidence = hardConfidence(rep, res)
	rep.Recommendation = a.recommendHard(rep)
	return rep
}

// convergeBoundaries is the heart of the count-driven detector. The metadata
// says there are exactly `need` internal chapter breaks and roughly where each
// one lives; the audio supplies candidates (file seams + detected silences). We
// ASSIGN each expected break its best nearby candidate — monotonic in time,
// distance-weighted, seams favoured over silences at like distance — so a
// surplus of long pauses in one region can never consume picks that belong to
// another region, and a CD rip's arbitrary track seams are simply never chosen
// for breaks they don't sit near. When the file's own silence floor doesn't
// surface a candidate for every expected break, the gate loosens one rung at a
// time (the "adjust the threshold until the count matches" loop). When even the
// loosest gate leaves expected breaks unmatched it returns the best assignment
// found with matched=false.
func convergeBoundaries(bookRungs []ladderRung, fileStarts, expected []float64, need int, p Params, total, toleranceSec float64) convergeResult {
	res := convergeResult{need: need}
	if need <= 0 {
		res.matched = true
		return res
	}

	seams := seamCandidates(fileStarts, p, total)
	seamTimes := make([]float64, len(seams))
	for i, s := range seams {
		seamTimes[i] = s.Time
	}

	attempt := func(rung ladderRung) []chapterCandidate {
		pool := append([]chapterCandidate(nil), seams...)
		for _, g := range validCandidates(rung.gaps, seamTimes, p, total) {
			pool = append(pool, gapCandidate(g, p))
		}
		sort.Slice(pool, func(i, j int) bool { return pool[i].Time < pool[j].Time })
		if len(expected) == need {
			return assignToExpected(pool, expected, toleranceSec, p.MergeWithinSeconds)
		}
		// Metadata had a count but no usable timestamps: fall back to the best
		// `need` candidates by intrinsic rank, spaced so merge can't change count.
		return topCandidates(pool, need, p.MergeWithinSeconds)
	}

	for _, rung := range bookRungs { // ascending gate offset
		chosen := attempt(rung)
		if len(chosen) < need {
			continue
		}
		res.chosen = chosen
		res.gateOffsetDB = rung.gateOffsetDB
		res.cutoffSec = minChosenGapDuration(rung.gaps, chosen)
		res.found = len(chosen)
		res.matched = true
		if rung.gateOffsetDB > 0 {
			res.note = fmt.Sprintf("loosened silence gate +%.0f dB to surface %d chapter breaks", rung.gateOffsetDB, need)
		}
		return res
	}

	// Even the loosest gate left expected breaks without a nearby candidate — a
	// continuous or music-bed recording, or a rip whose structure genuinely
	// differs from the matched edition. Return the best effort; the caller sees
	// matched=false and keeps the existing chapters.
	if len(bookRungs) > 0 {
		last := bookRungs[len(bookRungs)-1]
		chosen := attempt(last)
		res.chosen = chosen
		res.gateOffsetDB = last.gateOffsetDB
		res.cutoffSec = minChosenGapDuration(last.gaps, chosen)
		res.found = len(chosen)
		res.note = fmt.Sprintf("audio matched only %d of %d expected chapter breaks even at +%.0f dB gate", len(chosen), need, last.gateOffsetDB)
	}
	return res
}

// assignToExpected matches expected chapter positions to candidates with a
// monotonic minimum-cost alignment (same DP shape as alignNames): each expected
// break takes the candidate that costs least, costs are weighted distance
// (seams count as closer than silences at the same distance), matches may not
// cross in time, and no candidate is used twice. An expected break with no
// candidate within the drift tolerance goes unmatched — which the caller treats
// as "didn't converge at this gate" — rather than being force-fitted to
// something far away. Chosen candidates are also kept `spacing` apart so the
// final merge can't collapse two picks and silently change the count.
func assignToExpected(pool []chapterCandidate, expected []float64, toleranceSec, spacing float64) []chapterCandidate {
	n, k := len(pool), len(expected)
	if n == 0 || k == 0 {
		return nil
	}

	cost := func(candIdx, expIdx int) float64 {
		c := pool[candIdx]
		d := math.Abs(c.Time - expected[expIdx])
		if d > toleranceSec {
			return math.Inf(1) // out of reach: better to leave the break unmatched
		}
		if c.FromFile {
			d *= seamDistanceWeight
		}
		return d
	}

	const (
		moveSkipCand = 1 // candidate left unused (free)
		moveSkipExp  = 2 // expected break left unmatched (fails convergence; heavily penalised)
		moveMatch    = 3
	)
	// Skipping an expected break must be worse than any chain of real matches so
	// the DP only skips when nothing is in tolerance, yet finite so the alignment
	// still produces the best partial assignment for diagnostics.
	skipExpCost := (toleranceSec + 1) * float64(k+1)

	dp := make([][]float64, n+1)
	from := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]float64, k+1)
		from[i] = make([]int, k+1)
	}
	for j := 1; j <= k; j++ {
		dp[0][j] = dp[0][j-1] + skipExpCost
		from[0][j] = moveSkipExp
	}
	for i := 1; i <= n; i++ {
		dp[i][0] = 0
		from[i][0] = moveSkipCand
		for j := 1; j <= k; j++ {
			best := dp[i-1][j] // skip candidate
			move := moveSkipCand
			if c := dp[i][j-1] + skipExpCost; c < best {
				best = c
				move = moveSkipExp
			}
			if mc := cost(i-1, j-1); !math.IsInf(mc, 1) {
				if c := dp[i-1][j-1] + mc; c < best {
					best = c
					move = moveMatch
				}
			}
			dp[i][j] = best
			from[i][j] = move
		}
	}

	var chosen []chapterCandidate
	i, j := n, k
	for i > 0 || j > 0 {
		switch from[i][j] {
		case moveMatch:
			// Keep the candidate's own evidence flag: being near an expected
			// position doesn't make a 0.3 s desperation gap a confident pick.
			chosen = append(chosen, pool[i-1])
			i--
			j--
		case moveSkipExp:
			j--
		default:
			i--
		}
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].Time < chosen[j].Time })

	// Two expected breaks can in principle resolve to candidates closer together
	// than the merge window (e.g. a tiny credits chapter). Keep the earlier one;
	// the count comes up short and the caller reports the honest non-convergence.
	out := chosen[:0]
	for _, c := range chosen {
		if len(out) > 0 && c.Time-out[len(out)-1].Time <= spacing {
			continue
		}
		out = append(out, c)
	}
	return out
}

// topCandidates keeps the `need` best candidates by intrinsic rank (seams, then
// longest silences), mutually at least `spacing` apart, in time order. Fallback
// for hard targets whose metadata carried no usable timestamps.
func topCandidates(pool []chapterCandidate, need int, spacing float64) []chapterCandidate {
	if need <= 0 || len(pool) == 0 {
		return nil
	}
	ranked := append([]chapterCandidate(nil), pool...)
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].Time < ranked[j].Time
	})
	var chosen []chapterCandidate
	for _, c := range ranked {
		if len(chosen) == need {
			break
		}
		conflict := false
		for _, k := range chosen {
			if math.Abs(c.Time-k.Time) <= spacing {
				conflict = true
				break
			}
		}
		if !conflict {
			chosen = append(chosen, c)
		}
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].Time < chosen[j].Time })
	return chosen
}

// seamCandidates turns the file seams into boundary candidates: exact split
// offsets, edge-trimmed and deduped. Their rank (for the no-positions fallback)
// is seamBaseScore; under assignment their advantage is the distance discount.
func seamCandidates(fileStarts []float64, p Params, total float64) []chapterCandidate {
	edge := math.Max(p.MergeWithinSeconds, p.EdgeMarginSeconds)
	sorted := append([]float64(nil), fileStarts...)
	sort.Float64s(sorted)
	var out []chapterCandidate
	for _, s := range sorted {
		if s < edge || (total > 0 && s > total-edge) {
			continue
		}
		if len(out) > 0 && s-out[len(out)-1].Time <= p.MergeWithinSeconds {
			continue
		}
		out = append(out, chapterCandidate{Time: s, FromFile: true, score: seamBaseScore, supported: true})
	}
	return out
}

// gapCandidate ranks one detected silence: longer silences rank higher (chapter
// pauses out-last in-chapter pauses), with a little credit for how deep/clean
// the silence is. Supported marks gaps long enough to plausibly be a chapter
// pause on their own evidence.
func gapCandidate(g Gap, p Params) chapterCandidate {
	score := math.Log(math.Max(g.Duration, 1e-3))
	score += 0.15 * clampF(g.Depth, 0, 40) / 40
	t := g.MidSec()
	if p.BoundaryAtSilenceStart {
		t = g.StartSec
	}
	return chapterCandidate{Time: t, FromFile: false, score: score, supported: g.Duration >= 0.8}
}

// validCandidates drops silences that cannot be a real chapter start: too close
// to the book's edges (intro stinger / end-of-book silence) or sitting on a file
// seam (the seam candidate already represents that location, with an exact time).
func validCandidates(gaps []Gap, seamTimes []float64, p Params, total float64) []Gap {
	edge := math.Max(p.MergeWithinSeconds, p.EdgeMarginSeconds)
	var out []Gap
	for _, g := range gaps {
		if g.StartSec < edge || (total > 0 && g.StartSec > total-edge) {
			continue
		}
		if nearAny(g.StartSec, seamTimes, p.MergeWithinSeconds) {
			continue
		}
		out = append(out, g)
	}
	return out
}

// expectedInternalStarts is the list of approximate chapter-start positions the
// metadata expects, excluding the first chapter (it starts at 0, which is never
// an internal boundary). Used ONLY to bias candidate selection, never as a
// written position — the metadata timestamps drift, seams and silences do not.
func expectedInternalStarts(meta []catalog.AudioChapter) []float64 {
	var out []float64
	for i, c := range meta {
		if i == 0 || c.StartSeconds <= 0 {
			continue
		}
		out = append(out, c.StartSeconds)
	}
	sort.Float64s(out)
	return out
}

// metaCoverageSeconds is the metadata's own total runtime (last chapter end,
// falling back to last start) — compared against the real file duration to size
// the drift tolerance.
func metaCoverageSeconds(meta []catalog.AudioChapter) float64 {
	if len(meta) == 0 {
		return 0
	}
	last := meta[len(meta)-1]
	if last.EndSeconds > 0 {
		return last.EndSeconds
	}
	return last.StartSeconds
}

// minChosenGapDuration is the duration of the shortest chosen SILENCE — the
// effective gap-length threshold the convergence settled on. Seams have no
// duration; a book whose boundaries are all seams reports 0.
func minChosenGapDuration(gaps []Gap, chosen []chapterCandidate) float64 {
	m := math.Inf(1)
	for _, c := range chosen {
		if c.FromFile {
			continue
		}
		for _, g := range gaps {
			t := g.StartSec
			if math.Abs(t-c.Time) < 1e-6 || math.Abs(g.MidSec()-c.Time) < 1e-6 {
				if g.Duration < m {
					m = g.Duration
				}
				break
			}
		}
	}
	if math.IsInf(m, 1) {
		return 0
	}
	return m
}

// silenceSeparation reports how cleanly the kept silences stand out from the
// rejected ones (0..1) — the ratio of the shortest kept gap to the longest
// rejected gap, on a log scale. A wide band means the chapter breaks are
// unmistakable; a narrow one means the threshold cut through a continuum. Books
// whose boundaries are all seams have no silence cutoff and report 0.
func silenceSeparation(gaps []Gap, cutoff float64) float64 {
	if cutoff <= 0 {
		return 0
	}
	minKept := math.Inf(1)
	maxRej := 0.0
	for _, g := range gaps {
		if g.Duration >= cutoff {
			if g.Duration < minKept {
				minKept = g.Duration
			}
		} else if g.Duration > maxRej {
			maxRej = g.Duration
		}
	}
	if math.IsInf(minKept, 1) || maxRej <= 0 {
		return 1
	}
	return clampF((math.Log(minKept)-math.Log(maxRej))/math.Log(4), 0, 1)
}

// hardConfidence scores a count-driven proposal by how honestly it converged: the
// fraction of chosen boundaries that scored as plausible chapter breaks in their
// own right (supported), discounted by how far the silence gate had to be
// loosened. A clean book — every boundary a seam or long silence near an expected
// position, found at the file's own floor — reads ~0.95. A book where a chunk of
// the picks were desperation choices (short off-position gaps admitted at +12 dB)
// sinks below the apply threshold and goes to review instead.
func hardConfidence(rep *Report, res convergeResult) float64 {
	if !rep.CountMatched {
		return 0.3
	}
	if res.need <= 0 {
		return 0.9 // single-chapter work: nothing to find, nothing to doubt
	}
	supported := 0
	for _, c := range res.chosen {
		if c.supported {
			supported++
		}
	}
	frac := float64(supported) / float64(len(res.chosen))
	conf := 0.55 + 0.4*frac - 0.03*res.gateOffsetDB
	return clampF(conf, 0, 0.95)
}

// recommendHard maps a count-driven report to an apply/review/fallback verdict.
func (a *Analyzer) recommendHard(rep *Report) string {
	if rep.AudioCount <= 1 {
		return RecommendApply // single-chapter work
	}
	if rep.CountMatched && rep.Confidence >= a.Params.ApplyConfidence {
		return RecommendApply
	}
	if !rep.CountMatched {
		rep.Notes = append(rep.Notes, fmt.Sprintf(
			"audio produced %d chapter(s) but metadata expects %d — keeping existing chapters", rep.AudioCount, rep.TargetCount))
	}
	return RecommendReview
}

// titleInOrder assigns the i-th metadata title to chapter i directly. Used when
// the audio matched the metadata count exactly, so the two lists line up one for
// one and a straight positional mapping is both correct and order-stable.
func titleInOrder(i int, meta []catalog.AudioChapter) (string, bool) {
	if i >= 0 && i < len(meta) {
		if t := strings.TrimSpace(meta[i].Title); t != "" {
			return t, true
		}
	}
	return genericChapterTitle(i + 1), false
}

func nearAny(v float64, xs []float64, tol float64) bool {
	for _, x := range xs {
		if math.Abs(v-x) <= tol {
			return true
		}
	}
	return false
}

func nearestDist(v float64, xs []float64) float64 {
	best := math.Inf(1)
	for _, x := range xs {
		if d := math.Abs(v - x); d < best {
			best = d
		}
	}
	return best
}
