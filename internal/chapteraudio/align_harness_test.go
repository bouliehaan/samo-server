package chapteraudio

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// buildCDRipStair generates a known book, splits it into `files` arbitrary track
// files (seams deliberately NOT on chapter boundaries), and PREPENDS an
// accumulating sliver of room tone to each successive file — the per-file
// encoder/priming delay a real CD rip accrues. The chapter positions therefore
// drift further from the Audnexus MASTER timeline the deeper into the book you
// go, which is Case C: the failure the equal-slice e2e fixture cannot reproduce
// because it inserts no per-file delay.
//
// It returns the FileInputs, the MASTER chapter meta (Audnexus positions, which
// know nothing about the inserted slivers), and the TRUE chapter-start times in
// the concatenated USER timeline — what an accurate aligner must recover.
func buildCDRipStair(t *testing.T, dir string, chapters, files int, longPause, perFileDrift float64) ([]FileInput, []catalog.AudioChapter, []float64) {
	t.Helper()
	signal, mids := synthBook(chapters, longPause) // mids = true silence MIDS on the clean master
	const room = 0.0005

	// Split the clean master into `files` contiguous chunks at sample granularity.
	per := len(signal)/files + 1
	type chunk struct{ lo, hi, prefixSamples int }
	var chunks []chunk
	prefixSamples := 0
	for i := 0; i < files; i++ {
		lo := i * per
		hi := lo + per
		if hi > len(signal) {
			hi = len(signal)
		}
		if lo >= hi {
			break
		}
		// File 0 starts clean; every later file carries an extra priming sliver.
		if i > 0 {
			prefixSamples += int(perFileDrift * SampleRate)
		}
		chunks = append(chunks, chunk{lo: lo, hi: hi, prefixSamples: prefixSamples})
	}

	// Materialise each file = [accumulated-so-far priming sliver of room tone] + chunk.
	var inputs []FileInput
	rng := newDeterministicNoise()
	for i, c := range chunks {
		var buf []float32
		if i > 0 {
			n := int(perFileDrift * SampleRate)
			for j := 0; j < n; j++ {
				buf = append(buf, float32(room*rng()))
			}
		}
		buf = append(buf, signal[c.lo:c.hi]...)
		p := filepath.Join(dir, fmt.Sprintf("track%03d.wav", i+1))
		writeWAV16(t, p, buf)
		inputs = append(inputs, FileInput{Path: p, DurationSec: float64(len(buf)) / SampleRate})
	}

	// MASTER meta: chapter 1 at 0, the rest at their CLEAN master silence-start,
	// with EndSeconds chained (real Audnexus carries lengthMs, so master runtime is
	// known — the analyzer must not have to guess it from the last start).
	masterStart := func(i int) float64 { return mids[i] - longPause/2 } // i over internal boundaries
	masterTotal := float64(len(signal)) / SampleRate
	meta := []catalog.AudioChapter{{Index: 1, Title: "Chapter 1", StartSeconds: 0}}
	for i := range mids {
		meta = append(meta, catalog.AudioChapter{Index: i + 2, Title: fmt.Sprintf("Chapter %d", i+2), StartSeconds: masterStart(i)})
	}
	for i := range meta {
		if i+1 < len(meta) {
			meta[i].EndSeconds = meta[i+1].StartSeconds
		} else {
			meta[i].EndSeconds = masterTotal
		}
	}

	// TRUTH: the user-timeline position of each internal boundary = master sample +
	// the priming inserted in every file up to and including the one it lands in.
	prefixFor := func(masterSample int) int {
		for _, c := range chunks {
			if masterSample >= c.lo && masterSample < c.hi {
				return c.prefixSamples
			}
		}
		return chunks[len(chunks)-1].prefixSamples
	}
	var truth []float64
	for i := range mids {
		s := int(masterStart(i) * SampleRate)
		truth = append(truth, float64(s+prefixFor(s))/SampleRate)
	}
	return inputs, meta, truth
}

// buildRegistrationCase generates a clean known book (single file, crisp silences)
// and produces MASTER meta whose positions are the INVERSE warp of the file's true
// silences: meta = (trueFilePos - head) / scale. An aligner must recover (head,
// scale) to map the master positions back onto the real silences. This isolates
// the affine registration math (the audio itself is untouched, so any error is the
// warp's), covering the head-offset (missing/different brand intro) and re-encode
// scale cases the global affine is responsible for.
func buildRegistrationCase(t *testing.T, dir, name string, chapters int, longPause, head, scale float64) ([]FileInput, []catalog.AudioChapter, []float64) {
	t.Helper()
	signal, mids := synthBook(chapters, longPause)
	p := filepath.Join(dir, name+".wav")
	writeWAV16(t, p, signal)
	files := []FileInput{{Path: p, DurationSec: float64(len(signal)) / SampleRate}}

	trueStart := func(i int) float64 { return mids[i] - longPause/2 }
	fileTotal := float64(len(signal)) / SampleRate
	toMaster := func(fileT float64) float64 { return (fileT - head) / scale }

	meta := []catalog.AudioChapter{{Index: 1, Title: "Chapter 1", StartSeconds: 0}}
	for i := range mids {
		meta = append(meta, catalog.AudioChapter{Index: i + 2, Title: fmt.Sprintf("Chapter %d", i+2), StartSeconds: toMaster(trueStart(i))})
	}
	for i := range meta {
		if i+1 < len(meta) {
			meta[i].EndSeconds = meta[i+1].StartSeconds
		} else {
			meta[i].EndSeconds = toMaster(fileTotal)
		}
	}
	var truth []float64
	for i := range mids {
		truth = append(truth, trueStart(i))
	}
	return files, meta, truth
}

func runAlign(t *testing.T, files []FileInput, meta []catalog.AudioChapter) *Report {
	t.Helper()
	a := NewAnalyzer(ffmpegOrSkip(t))
	a.Params.HardTargetCount = true
	a.Params.BoundaryAtSilenceStart = true
	rep, err := a.AnalyzeBook(context.Background(), files, meta)
	if err != nil {
		t.Fatalf("AnalyzeBook: %v", err)
	}
	return rep
}

// TestRegistrationHeadOffset: the file lacks ~15s of head content the Audnexus
// master counts (a missing brand intro / opening credits). The affine head term
// must recover it and place every chapter on its real silence — the failure mode
// where "first chapters are already seconds off" comes straight from this.
func TestRegistrationHeadOffset(t *testing.T) {
	dir := t.TempDir()
	files, meta, truth := buildRegistrationCase(t, dir, "headoffset", 8, 1.4, -15.0, 1.0)
	rep := runAlign(t, files, meta)
	score := ScoreBoundaries(rep.Boundaries, truth)
	t.Logf("head-offset: head=%+.2fs scale=%.4f trusted=%v median=%.3fs p95=%.3fs within0.5=%.0f%%",
		rep.HeadOffsetSec, rep.ScaleFactor, rep.WarpTrusted, score.MedianAbs, score.P95Abs, score.Within0_5*100)
	if !rep.WarpTrusted {
		t.Fatalf("a clean single-file head offset must register (trusted), got untrusted")
	}
	if score.Within0_5 < 1.0 {
		t.Fatalf("head-offset: only %.0f%% within 0.5s (median %.3fs, worst %.3fs) — affine head must fix this", score.Within0_5*100, score.MedianAbs, score.WorstAbs)
	}
}

// TestRegistrationScale: a mild re-encode whose clock runs ~1.2% long. The affine
// scale term must absorb it so even deep chapters stay sub-second.
func TestRegistrationScale(t *testing.T) {
	dir := t.TempDir()
	files, meta, truth := buildRegistrationCase(t, dir, "scale", 10, 1.4, -3.0, 1.012)
	rep := runAlign(t, files, meta)
	score := ScoreBoundaries(rep.Boundaries, truth)
	t.Logf("scale: head=%+.2fs scale=%.4f trusted=%v median=%.3fs p95=%.3fs within0.5=%.0f%%",
		rep.HeadOffsetSec, rep.ScaleFactor, rep.WarpTrusted, score.MedianAbs, score.P95Abs, score.Within0_5*100)
	if !rep.WarpTrusted {
		t.Fatalf("a clean re-encode must register (trusted), got untrusted")
	}
	if score.Within0_5 < 1.0 {
		t.Fatalf("scale: only %.0f%% within 0.5s (median %.3fs) — affine scale must fix this", score.Within0_5*100, score.MedianAbs)
	}
}

// With drift correction ON, the accumulating-drift rip the affine alone declined
// must improve well past the ~4.37s fallback — the per-file onsets recover the
// inserted padding the seams cannot reveal.
func TestDriftCorrectionImprovesStair(t *testing.T) {
	ff := ffmpegOrSkip(t)
	dir := t.TempDir()
	inputs, meta, truth := buildCDRipStair(t, dir, 12, 24, 1.4, 0.4) // ~9.2s cumulative drift

	a := NewAnalyzer(ff)
	a.Params.HardTargetCount = true
	a.Params.BoundaryAtSilenceStart = true
	a.Params.DriftCorrection = true
	rep, err := a.AnalyzeBook(context.Background(), inputs, meta)
	if err != nil {
		t.Fatalf("AnalyzeBook: %v", err)
	}
	score := ScoreBoundaries(rep.Boundaries, truth)
	t.Logf("drift ON stair: measuredDrift=%.2fs trusted=%v median=%.3fs p95=%.3fs within0.5=%.0f%% within1.0=%.0f%%",
		rep.DriftSec, rep.WarpTrusted, score.MedianAbs, score.P95Abs, score.Within0_5*100, score.Within1_0*100)
	if score.MedianAbs >= 3.5 {
		t.Fatalf("drift correction did not improve the stair: median %.3fs (want well below the 4.37s affine-only fallback)", score.MedianAbs)
	}
}

// With drift correction ON, a rip whose seams fall on REAL pauses (no added
// padding) must measure ~zero drift and still converge — the trailing-silence
// rule must not mistake a seam-spanning pause for padding.
func TestDriftCorrectionIgnoresSeamSpanningPauses(t *testing.T) {
	ff := ffmpegOrSkip(t)
	const longPause = 2.5
	signal, mids := synthBook(3, longPause)
	dir := t.TempDir()
	per := len(signal)/6 + 1
	var files []FileInput
	for i := 0; i < 6; i++ {
		lo, hi := i*per, i*per+per
		if hi > len(signal) {
			hi = len(signal)
		}
		if lo >= hi {
			break
		}
		p := filepath.Join(dir, fmt.Sprintf("track%02d.wav", i+1))
		writeWAV16(t, p, signal[lo:hi])
		files = append(files, FileInput{Path: p, DurationSec: float64(hi-lo) / SampleRate})
	}
	meta := []catalog.AudioChapter{{Index: 1, Title: "One", StartSeconds: 0}}
	for i, mid := range mids {
		meta = append(meta, catalog.AudioChapter{Index: i + 2, Title: fmt.Sprintf("Ch %d", i+2), StartSeconds: mid - longPause/2})
	}
	a := NewAnalyzer(ff)
	a.Params.HardTargetCount = true
	a.Params.BoundaryAtSilenceStart = true
	a.Params.DriftCorrection = true
	rep, err := a.AnalyzeBook(context.Background(), files, meta)
	if err != nil {
		t.Fatalf("AnalyzeBook: %v", err)
	}
	t.Logf("seam-spanning pauses: measuredDrift=%.3fs audioCount=%d matched=%v", rep.DriftSec, rep.AudioCount, rep.CountMatched)
	if rep.DriftSec > 1.0 {
		t.Fatalf("seam-spanning pauses misread as %.2fs of drift — trailing-silence rule failed", rep.DriftSec)
	}
	if rep.AudioCount != 3 || !rep.CountMatched {
		t.Fatalf("drift-on must still converge a clean rip: count=%d matched=%v", rep.AudioCount, rep.CountMatched)
	}
}

// newDeterministicNoise returns a deterministic [-1,1) generator so fixtures are
// reproducible across runs (Date/rand seeding stays out of the signal).
func newDeterministicNoise() func() float64 {
	var x uint64 = 0x9e3779b97f4a7c15
	return func() float64 {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		return float64(int64(x)) / float64(1<<63) // ~[-1,1)
	}
}

// TestCDRipStairBaseline measures the CURRENT analyzer against a known CD-rip with
// accumulating per-file drift, using ScoreBoundaries — the regression baseline the
// alignment work will move. It asserts only that the metric machinery works and
// the count converges; the accuracy GATES are tightened as the warp engine lands,
// so this stays green while documenting where we start.
func TestCDRipStairBaseline(t *testing.T) {
	ff := ffmpegOrSkip(t)
	dir := t.TempDir()
	const (
		chapters     = 12
		files        = 24
		longPause    = 1.4
		perFileDrift = 0.25 // ~5.75s cumulative drift by the last file
	)
	inputs, meta, truth := buildCDRipStair(t, dir, chapters, files, longPause, perFileDrift)

	a := NewAnalyzer(ff)
	a.Params.HardTargetCount = true
	a.Params.BoundaryAtSilenceStart = true
	a.Params.DriftCorrection = false // this test is the AFFINE-ONLY baseline

	rep, err := a.AnalyzeBook(context.Background(), inputs, meta)
	if err != nil {
		t.Fatalf("AnalyzeBook: %v", err)
	}

	score := ScoreBoundaries(rep.Boundaries, truth)
	t.Logf("CD-rip stair baseline (drift off): chapters=%d files=%d cumulativeDrift=%.2fs", chapters, files, float64(files-1)*perFileDrift)
	t.Logf("  audioCount=%d target=%d matched=%v conf=%.2f", rep.AudioCount, rep.TargetCount, rep.CountMatched, rep.Confidence)
	t.Logf("  warp: head=%+.2fs scale=%.4f inlierFrac=%.2f trusted=%v", rep.HeadOffsetSec, rep.ScaleFactor, rep.WarpInlierFrac, rep.WarpTrusted)
	t.Logf("  median|err|=%.3fs  p95=%.3fs  worst=%.3fs  meanSigned=%+.3fs", score.MedianAbs, score.P95Abs, score.WorstAbs, score.MeanSigned)
	t.Logf("  within 0.2s=%.0f%%  0.5s=%.0f%%  1.0s=%.0f%%", score.Within0_2*100, score.Within0_5*100, score.Within1_0*100)
	for _, e := range score.Errors {
		if e.Signed > 0.5 || e.Signed < -0.5 {
			t.Logf("    ch%-2d truth=%.2fs placed=%.2fs err=%+.2fs", e.Index, e.Truth, e.Placed, e.Signed)
		}
	}

	if score.N == 0 {
		t.Fatalf("metric produced no comparisons (boundaries=%d truth=%d)", len(rep.Boundaries), len(truth))
	}
	// No-regression guard: a global affine cannot follow accumulating per-file drift,
	// so the warp must DECLINE here (untrusted) and the book must fall back to the
	// unregistered baseline (~4.4s median) — never the 6.8s the engine produced when
	// a band-capped scale was wrongly trusted. Phase 2 (per-file speech-onset drift)
	// is what actually drives this case sub-second; until then, do no harm.
	if score.MedianAbs > 5.0 {
		t.Fatalf("CD-rip stair regressed: median %.3fs > 5.0s — the affine over-fit accumulating drift instead of declining (warp trusted=%v)", score.MedianAbs, rep.WarpTrusted)
	}
}
