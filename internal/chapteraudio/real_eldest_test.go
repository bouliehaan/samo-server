package chapteraudio

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// TestRealEldestRegistration runs the registration engine over a slice of Jacob's
// ACTUAL Eldest CD rip (the first 26 mp3s, ~6384s, spanning Audnexus chapters
// 1-7) with the real Audnexus master timestamps as the target. It is skipped
// unless the files have been staged at /tmp/eldest_real/fNNN.mp3. It is a manual
// validation probe, not a CI gate: it prints what the engine did and asserts the
// headline property — placed boundaries must DIFFER from the pasted raw-Audnexus
// timestamps and land on REAL detected silences.
func TestRealEldestRegistration(t *testing.T) {
	ff := ffmpegOrSkip(t)
	dir := "/tmp/eldest_real"
	if _, err := os.Stat(filepath.Join(dir, "f001.mp3")); err != nil {
		t.Skip("real Eldest files not staged at /tmp/eldest_real")
	}

	var inputs []FileInput
	for i := 1; i <= 26; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%03d.mp3", i))
		if _, err := os.Stat(p); err != nil {
			break
		}
		inputs = append(inputs, FileInput{Path: p})
	}
	t.Logf("real Eldest slice: %d files", len(inputs))

	// Real Audnexus Eldest (B003WVNWU4) chapters 1-7. The audio reaches ~master
	// 6403s (file 6383.6s + a ~20s missing intro), so chapter 7 is truncated — its
	// end is clamped to the audio's master extent for the end-anchor.
	starts := []float64{0, 57.430, 1119.872, 2620.785, 3770.564, 4949.717, 5938.909}
	titles := []string{"Opening Credits", "A Twin Disaster", "The Council of Elders", "Truth Among Friends", "Roran", "The Hunted Hunters", "Saphira's Promise"}
	const masterExtent = 6403.0
	var meta []catalog.AudioChapter
	for i := range starts {
		end := masterExtent
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		meta = append(meta, catalog.AudioChapter{Index: i + 1, Title: titles[i], StartSeconds: starts[i], EndSeconds: end})
	}

	a := NewAnalyzer(ff)
	a.Params.HardTargetCount = true
	a.Params.BoundaryAtSilenceStart = true
	// DriftCorrection defaults on.

	rep, err := a.AnalyzeBook(context.Background(), inputs, meta)
	if err != nil {
		t.Fatalf("AnalyzeBook: %v", err)
	}

	t.Logf("decoded runtime: %.1fs", rep.DurationSec)
	t.Logf("registration: trusted=%v head=%+.2fs scale=%.4f drift=%.2fs inlierFrac=%.2f recommendation=%s conf=%.2f",
		rep.WarpTrusted, rep.HeadOffsetSec, rep.ScaleFactor, rep.DriftSec, rep.WarpInlierFrac, rep.Recommendation, rep.Confidence)
	t.Logf("count: audio=%d target=%d matched=%v", rep.AudioCount, rep.TargetCount, rep.CountMatched)

	gapStarts := make([]float64, len(rep.Gaps))
	for i, g := range rep.Gaps {
		gapStarts[i] = g.StartSec
	}

	t.Logf("%-3s %12s %12s %10s %12s", "ch", "rawAudnexus", "placed", "Δ-vs-raw", "→nearestSil")
	rawInternal := starts[1:]
	movedOff, onSilence := 0, 0
	for i, b := range rep.Boundaries {
		raw := math.NaN()
		if i < len(rawInternal) {
			raw = rawInternal[i]
		}
		dSil := nearestDist(b, gapStarts)
		t.Logf("%-3d %12.2f %12.2f %+10.2f %12.2f", i+2, raw, b, b-raw, dSil)
		if !math.IsNaN(raw) && math.Abs(b-raw) > 1.0 {
			movedOff++
		}
		if dSil < 1.0 {
			onSilence++
		}
	}

	if len(rep.Boundaries) == 0 {
		t.Fatal("no boundaries produced")
	}
	// Headline: registration must MOVE every boundary off the pasted raw-Audnexus
	// timestamp, and the bulk must land on real silences. The irregular intro region
	// (a different credits length than the master) legitimately has no pause at its
	// predicted spot, so allow up to ~2 boundaries to be warp-interpolated rather
	// than snapped — that is the honest "flag, don't fake" outcome, not a failure.
	if movedOff != len(rep.Boundaries) {
		t.Errorf("only %d/%d boundaries moved off raw Audnexus — registration did not engage", movedOff, len(rep.Boundaries))
	}
	if onSilence < len(rep.Boundaries)-2 {
		t.Errorf("only %d/%d boundaries land on a real silence (want >= %d)", onSilence, len(rep.Boundaries), len(rep.Boundaries)-2)
	}
	if !rep.CountMatched {
		t.Errorf("count did not match — off-by-one or a dropped boundary")
	}
	t.Logf("RESULT: %d/%d boundaries moved >1s off raw Audnexus; %d/%d land on a real silence",
		movedOff, len(rep.Boundaries), onSilence, len(rep.Boundaries))
}
