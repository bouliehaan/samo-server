package chapteraudio

import (
	"math"
	"sort"
)

// The registration core. The Audnexus chapter starts are timed against the
// Audible MASTER edition; the user's files are a different cut (missing/extra
// brand intro, a re-encode's sample-rate drift, a CD rip's per-file priming). So
// the master timestamps must never be written as-is — they are REGISTERED onto
// this file by solving a monotone time-warp master→file and then snapping each
// warped prediction to a real pause. This file estimates the GLOBAL affine term
// (head offset + scale); the per-file-onset local drift correction (Case C) layers
// on top of it.

// driftModel captures the accumulating per-file leading-silence drift of a
// multi-file rip: content in file k sits cumDrift[k] seconds LATER in the
// concatenated file timeline than in a gapless master. The registration is fitted
// in the DE-DRIFTED "master-content" clock (where this nonlinear, per-file delay
// is removed and a single affine warp suffices); chosen boundaries are converted
// back to real file time. With one file, no measured onsets, or a gapless rip it
// is inactive and every conversion is the identity, so single-file and gapless
// books are untouched.
type driftModel struct {
	fileStart []float64 // file k spans [fileStart[k], fileStart[k+1]) in FILE seconds; len nFiles+1
	cumDrift  []float64 // cumulative leading silence through file k; len nFiles
	master    []float64 // de-drifted start of each file's content (master-content clock); len nFiles
	total     float64   // total accumulated drift
}

// driftSeamSilenceFloor: a previous-file trailing silence at/above this is taken
// as a real pause spanning the seam (so this file's leading silence is NOT counted
// as added drift) rather than the file ending mid-speech.
const driftSeamSilenceFloor = 0.2

// buildDriftModel folds the per-file speech onsets into the cumulative ADDED drift
// and the de-drifted content-start of each file. A file's leading silence counts
// as drift only when the previous file ended in speech; otherwise it is the tail
// of a real pause that spans the seam (present in the master too) and contributes
// nothing. File 0's head is never drift — that offset is the affine head term.
func buildDriftModel(files []FileAnalysis) driftModel {
	dm := driftModel{fileStart: []float64{0}}
	acc, running := 0.0, 0.0
	prevTrailing := -1.0 // sentinel for "no previous file"
	for _, fa := range files {
		contribution := 0.0
		if prevTrailing >= 0 && prevTrailing < driftSeamSilenceFloor {
			contribution = fa.SpeechOnsetSec
		}
		acc += contribution
		dm.cumDrift = append(dm.cumDrift, acc)
		// masterContentStart_k = (fileStart_k + onset_k) − cumDrift_k.
		dm.master = append(dm.master, running+fa.SpeechOnsetSec-acc)
		running += fa.DurationSec
		dm.fileStart = append(dm.fileStart, running)
		prevTrailing = fa.TrailingSilenceSec
	}
	dm.total = acc
	return dm
}

// active reports whether there is meaningful multi-file drift to correct.
func (dm driftModel) active() bool { return len(dm.cumDrift) > 1 && dm.total > 0 }

func lastIndexAtMost(bounds []float64, v float64, n int) int {
	k := 0
	for k+1 < n && bounds[k+1] <= v {
		k++
	}
	return k
}

// deDrift maps a FILE-time to the de-drifted master-content clock.
func (dm driftModel) deDrift(t float64) float64 {
	if !dm.active() {
		return t
	}
	return t - dm.cumDrift[lastIndexAtMost(dm.fileStart, t, len(dm.cumDrift))]
}

// reDrift maps a de-drifted master-content time back to real FILE time.
func (dm driftModel) reDrift(m float64) float64 {
	if !dm.active() {
		return m
	}
	return m + dm.cumDrift[lastIndexAtMost(dm.master, m, len(dm.cumDrift))]
}

// deDriftRungs shifts every detected silence into the de-drifted clock so the
// gate-ladder search and the warp fit see a continuous, registerable timeline.
func (dm driftModel) deDriftRungs(rungs []ladderRung) []ladderRung {
	if !dm.active() {
		return rungs
	}
	out := make([]ladderRung, len(rungs))
	for i, r := range rungs {
		out[i].gateOffsetDB = r.gateOffsetDB
		out[i].gaps = make([]Gap, len(r.gaps))
		for j, g := range r.gaps {
			d := dm.cumDrift[lastIndexAtMost(dm.fileStart, g.StartSec, len(dm.cumDrift))]
			g.StartSec -= d
			g.EndSec -= d
			out[i].gaps[j] = g
		}
	}
	return out
}

// deDriftedFileStarts returns the content-starts of files after the first in the
// de-drifted clock (the seam candidates the warp/converge see).
func (dm driftModel) deDriftedFileStarts() []float64 {
	if !dm.active() || len(dm.master) < 2 {
		return nil
	}
	return append([]float64(nil), dm.master[1:]...)
}

// affineWarp maps master seconds to file seconds: file_t = Scale*master_t + Head.
type affineWarp struct {
	Head        float64 // seconds the file is shifted vs the master (negative = file starts earlier, e.g. no brand intro)
	Scale       float64 // file/master time ratio (≈1; absorbs re-encode/resample drift)
	Inliers     int     // master chapters that landed within tolerance of a real candidate under this fit
	Total       int     // master internal chapters considered
	ResidualStd float64 // RMS of inlier snap distances — sizes the downstream snap window
	Trusted     bool    // the fit explains enough chapters to register from; if false, callers fall back to the raw prior
}

func (w affineWarp) predict(masterT float64) float64 { return w.Scale*masterT + w.Head }

func (w affineWarp) inlierFraction() float64 {
	if w.Total == 0 {
		return 0
	}
	return float64(w.Inliers) / float64(w.Total)
}

// estimateAffineWarp robustly fits file_t = Scale*master_t + Head from the master
// internal chapter starts (`expected`) and the file's own candidate boundary times
// (`candTimes` = detected silence starts + file seams). Decoy pauses (dramatic
// in-chapter silences) are rejected: the head is the MODE of the per-chapter
// master→nearest-candidate offset, not its mean, so a cluster of true chapters
// outvotes scattered decoys. Scale is seeded from the runtime ratio and only
// refined within a tight band — a book is a re-cut of the same narration, not a
// 2x speed change. When too few chapters agree, Trusted is false and the caller
// keeps the raw prior rather than registering off a bad fit.
//
// masterRuntime/fileRuntime are the two editions' total seconds; their difference
// end-anchors the head. inlierTol is the snap distance within which a master
// chapter counts as explained while fitting.
func estimateAffineWarp(expected, candTimes []float64, masterRuntime, fileRuntime, inlierTol float64) affineWarp {
	// Global scale absorbs only true re-encode/resample drift (well under a
	// percent) — NEVER a CD rip's accumulating per-file delay, which is the local
	// drift term's job (Phase 2). Letting scale balloon to swallow accumulating
	// drift is what makes a stair-drift rip WORSE: it over-predicts deep chapters.
	// So scale stays pinned near 1; a runtime difference is treated as a head term
	// (missing/extra intro), not a stretch.
	const maxScaleDev = 0.02
	loScale, hiScale := 1-maxScaleDev, 1+maxScaleDev
	w := affineWarp{Head: 0, Scale: 1, Total: len(expected)}
	if len(expected) < 2 || len(candTimes) < 2 {
		return w // not enough evidence to register; identity, untrusted
	}
	sorted := append([]float64(nil), candTimes...)
	sort.Float64s(sorted)

	// END-ANCHORED head seed: with scale≈1 the file is shifted from the master by
	// the difference in their total runtimes. This pins head GLOBALLY and breaks the
	// off-by-one aliasing that pure nearest-pause matching suffers when the head
	// offset is comparable to the chapter spacing (a long opening-credits block):
	// snapping each master start to its nearest pause is equally happy one chapter
	// over, but only the true shift makes the WHOLE sequence (first and last
	// included) land in bounds. We therefore look for the head cluster NEAR this
	// seed, not the globally densest one.
	headSeed := 0.0
	if masterRuntime > 0 && fileRuntime > 0 {
		headSeed = fileRuntime - masterRuntime
	}
	win := clampF(0.4*medianSpacing(expected), 2.0, 8.0)

	// Residuals around the SEEDED prediction: r_i = nearestCandidate(scale·m_i +
	// headSeed) − (scale·m_i + headSeed). The seed must be applied to the prediction
	// — not just used to pick among raw nearest-pause offsets — or nearestCandidate
	// keeps grabbing the off-by-one neighbour one chapter away and the true shift is
	// never seen. With a good seed the residuals cluster at 0; their median is the
	// head correction.
	residualsAt := func(scale, head float64) []float64 {
		out := make([]float64, 0, len(expected))
		for _, m := range expected {
			p := scale*m + head
			out = append(out, nearestSorted(sorted, p)-p)
		}
		return out
	}

	// FIT at the loose tolerance — generous enough to gather correspondences across
	// a missing brand intro and mild drift.
	fitInliers := func(scale, head float64) int {
		n, _ := countInliers(expected, sorted, scale, head, inlierTol)
		return n
	}
	w.Head = headSeed + clusterNear(residualsAt(w.Scale, headSeed), 0, win)
	baseIn := fitInliers(w.Scale, w.Head)
	refined := refineAffine(expected, sorted, w.Scale, w.Head, inlierTol)
	if refined.Scale >= loScale && refined.Scale <= hiScale {
		if fitInliers(refined.Scale, refined.Head) >= baseIn {
			w.Scale, w.Head = refined.Scale, refined.Head
		}
	}

	// VERIFY at a TIGHT tolerance. A genuine registration lines the chapters up to
	// a fraction of a second; a spurious one (e.g. predictions that merely fall near
	// the regularly-spaced CD seams, or a band-capped scale that can't follow real
	// accumulating drift) lines up only loosely. Trusting on the loose count is how
	// a confident-looking but wrong warp slips through, so trust — and the snap
	// window — is sized on the tight inliers, and only their residual.
	// Verify at a TIGHT tolerance. A genuine registration lines the chapters up to a
	// fraction of a second; a spurious one (predictions merely near the regularly-
	// spaced CD seams, or a band-capped scale that can't follow accumulating drift)
	// lines up only loosely. Trust — and the snap window — is sized on the tight
	// inliers so a near-aligned-but-jittery rip stays on the loose fallback (which
	// snaps each boundary in a wide window) rather than the tight per-chapter path
	// that would interpolate its jitter. Big-offset rips, where the loose nearest-
	// match would mis-snap, clear this bar and get the robust per-chapter placement.
	trustTol := inlierTol / 3
	if trustTol < 0.5 {
		trustTol = 0.5
	}
	w.Inliers, w.ResidualStd = countInliers(expected, sorted, w.Scale, w.Head, trustTol)
	w.Trusted = w.inlierFraction() >= 0.6 && w.Inliers >= 3
	return w
}

// clusterNear returns the median of the offsets within `win` of `center` — the
// head term consistent with the end-anchor. Falls back to the globally densest
// cluster only when nothing lands near the seed (e.g. runtimes unknown).
func clusterNear(offsets []float64, center, win float64) float64 {
	var near []float64
	for _, o := range offsets {
		if math.Abs(o-center) <= win {
			near = append(near, o)
		}
	}
	if len(near) == 0 {
		return modeCluster(offsets, win)
	}
	sort.Float64s(near)
	return median(near)
}

// medianSpacing is the median gap between consecutive expected chapter starts —
// the aliasing period the head search must stay inside half of.
func medianSpacing(expected []float64) float64 {
	if len(expected) < 2 {
		return 0
	}
	diffs := make([]float64, 0, len(expected)-1)
	for i := 1; i < len(expected); i++ {
		diffs = append(diffs, expected[i]-expected[i-1])
	}
	sort.Float64s(diffs)
	return median(diffs)
}

// modeCluster returns the center of the densest cluster of offsets within a window
// of half-width `tol`: slide a width-2·tol window, pick the position covering the
// most points, return the median of the points inside it. This is a cheap 1-D
// Hough for the head term that is immune to a minority of decoy correspondences.
func modeCluster(offsets []float64, tol float64) float64 {
	if len(offsets) == 0 {
		return 0
	}
	s := append([]float64(nil), offsets...)
	sort.Float64s(s)
	bestLo, bestCount := 0, 0
	j := 0
	for i := range s {
		for j < len(s) && s[j]-s[i] <= 2*tol {
			j++
		}
		if j-i > bestCount {
			bestCount = j - i
			bestLo = i
		}
	}
	window := s[bestLo : bestLo+bestCount]
	return median(window)
}

// refineAffine does one least-squares fit over the correspondences that the
// current (scale,head) explains, returning the sharpened parameters.
func refineAffine(expected, sortedCand []float64, scale, head, tol float64) affineWarp {
	var xs, ys []float64
	for _, m := range expected {
		p := scale*m + head
		c := nearestSorted(sortedCand, p)
		if math.Abs(c-p) <= tol {
			xs = append(xs, m)
			ys = append(ys, c)
		}
	}
	if len(xs) < 2 {
		return affineWarp{Scale: scale, Head: head}
	}
	s, h := leastSquaresLine(xs, ys)
	return affineWarp{Scale: s, Head: h}
}

func countInliers(expected, sortedCand []float64, scale, head, tol float64) (int, float64) {
	var n int
	var sumsq float64
	for _, m := range expected {
		p := scale*m + head
		d := math.Abs(nearestSorted(sortedCand, p) - p)
		if d <= tol {
			n++
			sumsq += d * d
		}
	}
	if n == 0 {
		return 0, 0
	}
	return n, math.Sqrt(sumsq / float64(n))
}

// leastSquaresLine fits y = a·x + b, returning (a, b).
func leastSquaresLine(xs, ys []float64) (a, b float64) {
	n := float64(len(xs))
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
	}
	den := n*sxx - sx*sx
	if math.Abs(den) < 1e-9 {
		return 1, 0
	}
	a = (n*sxy - sx*sy) / den
	b = (sy - a*sx) / n
	return a, b
}

// nearestSorted returns the element of a sorted slice closest to v.
func nearestSorted(sorted []float64, v float64) float64 {
	n := len(sorted)
	if n == 0 {
		return v
	}
	i := sort.SearchFloat64s(sorted, v)
	if i == 0 {
		return sorted[0]
	}
	if i >= n {
		return sorted[n-1]
	}
	if v-sorted[i-1] <= sorted[i]-v {
		return sorted[i-1]
	}
	return sorted[i]
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
