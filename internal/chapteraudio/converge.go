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

	need := count - 1
	expected := expectedInternalStarts(meta)

	// REGISTER the master chapter sequence onto this file before snapping. The
	// Audnexus starts are MASTER-edition times; pasting them is the drift bug.
	// Estimate the affine warp (head + scale) from the file's own candidate pauses
	// and snap the WARPED predictions in a TIGHT window. The systematic head/scale
	// offset is absorbed by the warp, not by a runtime-sized tolerance that lets a
	// dramatic pause masquerade as a chapter break.
	metaCov := metaCoverageSeconds(meta)

	// REMOVE the accumulating per-file drift first. A CD rip's leading-silence
	// priming makes the master→file map NONLINEAR — a single affine cannot follow
	// it (that is why affine-only declines a stair rip). We measure each file's
	// leading silence from its OWN audio (independent of the cumulative duration
	// sum that embeds the drift) and shift every candidate into the de-drifted
	// "master-content" clock, where one affine warp suffices. Chosen boundaries are
	// converted back to real file time at the end. Inactive (identity) for
	// single-file books and gapless rips.
	dm := buildDriftModel(rep.Files)
	rep.DriftSec = dm.total
	wRungs, wFileStarts, wTotal := bookRungs, fileStarts, rep.DurationSec
	// Drift correction is gated OFF by default: distinguishing inserted per-file
	// padding (real drift) from a real pause that merely SPANS a file seam (not
	// drift, the master carries it too) needs calibration against the golden set —
	// counting a spanning pause as drift double-shifts and makes deep chapters
	// worse. Until that's calibrated, the affine backbone ships alone and an
	// accumulating-drift rip safely declines to the unregistered fallback.
	useDrift := a.Params.DriftCorrection && dm.active()
	if useDrift {
		wRungs = dm.deDriftRungs(bookRungs)
		wFileStarts = dm.deDriftedFileStarts()
		wTotal = rep.DurationSec - dm.total
	}

	// REGISTER the master chapter sequence onto the (de-drifted) file timeline
	// before snapping. The Audnexus starts are MASTER-edition times; pasting them is
	// the drift bug. Fit against the strongest pauses only (seams + longest silences,
	// ~3× the chapter count) so the registration locks onto real breaks, not the
	// decoy cloud; the end-anchor uses the TRUE master runtime (Audnexus lengthMs).
	candTimes := strongCandidateTimes(wRungs[0], wFileStarts, a.Params, wTotal, 3*need+4)
	warp := estimateAffineWarp(expected, candTimes, metaRuntimeSeconds(meta), wTotal, a.Params.WarpInlierTolSec)
	rep.HeadOffsetSec = warp.Head
	rep.ScaleFactor = warp.Scale
	rep.WarpInlierFrac = warp.inlierFraction()
	rep.WarpTrusted = warp.Trusted

	searchExpected := expected
	// Fallback tolerance (registration NOT trusted): the metadata's own timing
	// error widens the search so a deep position off by the edition gap still finds
	// its candidate. Only used when the warp could not be fit confidently.
	tolerance := a.Params.PositionDriftToleranceSec
	if metaCov > 0 && wTotal > 0 {
		if delta := math.Abs(metaCov - wTotal); delta > tolerance {
			tolerance = delta
		}
	}
	if warp.Trusted {
		var chosen []chapterCandidate
		var snapped int
		note := ""
		if useDrift {
			// PER-FILE RIP with accumulating drift: the warp's drift correction is
			// approximate, so pin each chapter to a real pause near its prediction —
			// the silences carry the residual the warp couldn't model. Tight,
			// fit-derived window; place-by-identity (interpolate a miss in place) so a
			// nameless intro can't slide every later label one chapter over.
			tolerance = clampF(4*warp.ResidualStd, a.Params.WarpSnapFloorSec, a.Params.WarpSnapCapSec)
			pool := placementPool(wRungs[len(wRungs)-1], wFileStarts, a.Params, wTotal)
			chosen, snapped = placeByWarp(warp, expected, pool, tolerance)
			note = fmt.Sprintf("registered (drift %.2fs): head %+.2fs scale %.4f; placed %d/%d on a real pause (%d by warp)",
				dm.total, warp.Head, warp.Scale, snapped, need, need-snapped)
		} else {
			// AFFINE MAP (single-file edition, clean re-encode, gapless rip): the warp
			// maps each Audnexus marker — which Audible placed at the CHAPTER-NAME ONSET
			// — straight onto this file. Do NOT snap to the nearest silence: that drags
			// the boundary back to the start of the preceding pause, seconds off the
			// name (the bug where single-file editions landed ~4s early and you missed
			// "Chapter One"). Trust the registered position; it IS where Audible marks it.
			chosen = placeAtWarpDirect(warp, expected)
			snapped = len(expected)
			note = fmt.Sprintf("registered (affine, no snap): head %+.2fs scale %.4f; placed %d Audnexus markers onto the file",
				warp.Head, warp.Scale, need)
		}

		var cands []boundaryCand
		for _, c := range chosen {
			t := c.Time
			if useDrift {
				t = dm.reDrift(c.Time)
			}
			cands = append(cands, boundaryCand{Time: t, FromFile: c.FromFile})
		}
		merged := mergeBoundaries(cands, a.Params.MergeWithinSeconds, a.Params.EdgeMarginSeconds, rep.DurationSec)
		exact := len(merged)+1 == count
		a.fillBoundaries(rep, merged, meta, exact)
		rep.CountMatched = exact
		rep.GateOffsetDB = warp.Head // record the registration, not a gate
		frac := 1.0
		if need > 0 {
			frac = float64(snapped) / float64(need)
		}
		rep.Confidence = clampF(0.55+0.4*frac, 0, 0.97)
		rep.Notes = append(rep.Notes, note)
		rep.Recommendation = a.recommendHard(rep)
		return rep
	}

	// Registration not trusted: fall back to the loose nearest-candidate assignment
	// with the wide tolerance, exactly as before the warp.
	res := convergeBoundaries(wRungs, wFileStarts, searchExpected, need, a.Params, wTotal, tolerance)
	rep.GateOffsetDB = res.gateOffsetDB
	rep.SplitSeconds = res.cutoffSec
	if res.note != "" {
		rep.Notes = append(rep.Notes, res.note)
	}

	// Convert the chosen boundaries from the de-drifted clock back to real file time.
	var cands []boundaryCand
	for _, c := range res.chosen {
		t := c.Time
		if useDrift {
			t = dm.reDrift(c.Time)
		}
		cands = append(cands, boundaryCand{Time: t, FromFile: c.FromFile})
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

// placementPool builds the candidate boundaries (file seams + valid silences at
// the given rung) for warp-anchored per-chapter placement.
func placementPool(rung ladderRung, fileStarts []float64, p Params, total float64) []chapterCandidate {
	seams := seamCandidates(fileStarts, p, total)
	seamTimes := make([]float64, len(seams))
	for i, s := range seams {
		seamTimes[i] = s.Time
	}
	pool := append([]chapterCandidate(nil), seams...)
	for _, g := range validCandidates(rung.gaps, seamTimes, p, total) {
		pool = append(pool, gapCandidate(g, p))
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].Time < pool[j].Time })
	return pool
}

// placeAtWarpDirect places each chapter at its warped Audnexus position with NO
// silence snapping — for affine maps where the warp is the truth and Audnexus
// already sits at the chapter-name onset. Monotonic by construction (predictions
// increase with the expected starts).
func placeAtWarpDirect(w affineWarp, expected []float64) []chapterCandidate {
	out := make([]chapterCandidate, 0, len(expected))
	prev := math.Inf(-1)
	for _, m := range expected {
		t := w.predict(m)
		if t <= prev {
			t = prev + 0.001
		}
		out = append(out, chapterCandidate{Time: t, supported: true})
		prev = t
	}
	return out
}

// placeByWarp places each expected chapter at its warped prediction, snapping to
// the nearest pool candidate within tol that keeps the sequence strictly
// increasing, else leaving it at the prediction (interpolated). Returns the chosen
// candidates (time order) and how many snapped to a real pause. Because each
// chapter is placed independently by its own identity, a chapter with no nearby
// pause is interpolated in place rather than shifting every later chapter's label.
func placeByWarp(w affineWarp, expected []float64, pool []chapterCandidate, tol float64) ([]chapterCandidate, int) {
	chosen := make([]chapterCandidate, 0, len(expected))
	snapped := 0
	prev := math.Inf(-1)
	for _, m := range expected {
		p := w.predict(m)
		best := -1
		bestD := tol
		for k, c := range pool {
			if c.Time <= prev {
				continue
			}
			if d := math.Abs(c.Time - p); d <= bestD {
				bestD, best = d, k
			}
		}
		if best >= 0 {
			chosen = append(chosen, pool[best])
			prev = pool[best].Time
			snapped++
			continue
		}
		t := p
		if t <= prev {
			t = prev + 0.001
		}
		chosen = append(chosen, chapterCandidate{Time: t, supported: false})
		prev = t
	}
	return chosen, snapped
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

// strongCandidateTimes returns the candidate boundary times most likely to be
// REAL chapter breaks — every file seam plus the `keep` longest detected silences
// — for FITTING the affine warp. Fitting against only the strongest pauses keeps
// the head/scale estimate from locking onto the dense cloud of short in-chapter
// pauses: with enough decoys, almost any head fits "something" nearby, so an
// unfiltered pool yields a confident-looking but wrong registration. (When chapter
// pauses are NOT longer than paragraph pauses — e.g. a flat narrator — even this
// is ambiguous; that is the case the per-file-onset drift and ASR tiers exist for.)
func strongCandidateTimes(rung ladderRung, fileStarts []float64, p Params, total float64, keep int) []float64 {
	seams := seamCandidates(fileStarts, p, total)
	seamTimes := make([]float64, len(seams))
	for i, s := range seams {
		seamTimes[i] = s.Time
	}
	out := append([]float64(nil), seamTimes...)
	gaps := validCandidates(rung.gaps, seamTimes, p, total)
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].Duration > gaps[j].Duration })
	if keep > len(gaps) {
		keep = len(gaps)
	}
	for _, g := range gaps[:keep] {
		out = append(out, gapCandidate(g, p).Time)
	}
	return out
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

// metaRuntimeSeconds is the master edition's TRUE total runtime: the last
// chapter's end (Audnexus lengthMs). Unlike metaCoverageSeconds it does NOT fall
// back to the last start — a fabricated runtime would corrupt the warp's
// end-anchor — so it returns 0 when end times are absent, signalling "no anchor".
func metaRuntimeSeconds(meta []catalog.AudioChapter) float64 {
	if len(meta) == 0 {
		return 0
	}
	return meta[len(meta)-1].EndSeconds
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
