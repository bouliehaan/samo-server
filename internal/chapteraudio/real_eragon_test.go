package chapteraudio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// TestRealEragonRegistration validates the engine on the HARDER real case:
// Jacob's Eragon CD rip is ~1.85 files per chapter (vs Eldest's ~4.8), so chapter
// boundaries fall mid-file far more often and must be found from silences, not
// seams. It reads the first staged mp3s + the saved Audnexus chapter list from
// /tmp/eragon_real, registers chapters whose master start is comfortably inside
// the staged audio, and asserts the bulk land on real silences with correct labels.
// Skipped unless the files are staged. Manual probe, not a CI gate.
func TestRealEragonRegistration(t *testing.T) {
	ff := ffmpegOrSkip(t)
	dir := "/tmp/eragon_real"
	if _, err := os.Stat(filepath.Join(dir, "f001.mp3")); err != nil {
		t.Skip("real Eragon files not staged at /tmp/eragon_real")
	}

	var inputs []FileInput
	for i := 1; i <= 30; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%03d.mp3", i))
		if _, err := os.Stat(p); err != nil {
			break
		}
		inputs = append(inputs, FileInput{Path: p})
	}

	starts, titles := loadAudnexStarts(t, filepath.Join(dir, "audnex.json"))
	// Staged audio decodes to ~14616.6s. Include every chapter whose master start is
	// inside that extent and set the LAST chapter's end to the real file extent, so
	// the registration's end-anchor sees matching runtimes (as it would for the full
	// book in production) — a truncated meta against full files would distrust the
	// warp and fall back to the loose path.
	const fileExtent = 14616.6
	var meta []catalog.AudioChapter
	for i := range starts {
		if i > 0 && starts[i] >= fileExtent-30 {
			break
		}
		end := fileExtent
		if i+1 < len(starts) && starts[i+1] < fileExtent-30 {
			end = starts[i+1]
		}
		meta = append(meta, catalog.AudioChapter{Index: i + 1, Title: titles[i], StartSeconds: starts[i], EndSeconds: end})
	}
	t.Logf("real Eragon slice: %d files, %d chapters in meta", len(inputs), len(meta))

	a := NewAnalyzer(ff)
	a.Params.HardTargetCount = true
	a.Params.BoundaryAtSilenceStart = true

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
	rawInternal := starts[1:len(meta)]
	movedOff, onSilence := 0, 0
	t.Logf("%-3s %12s %12s %10s %12s", "ch", "rawAudnexus", "placed", "Δ-vs-raw", "→nearestSil")
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
	if !rep.CountMatched {
		t.Errorf("count did not match — off-by-one or a dropped boundary")
	}
	if onSilence < len(rep.Boundaries)-2 {
		t.Errorf("only %d/%d boundaries land on a real silence (want >= %d)", onSilence, len(rep.Boundaries), len(rep.Boundaries)-2)
	}
	t.Logf("RESULT: %d/%d moved >1s off raw Audnexus; %d/%d on a real silence",
		movedOff, len(rep.Boundaries), onSilence, len(rep.Boundaries))
}

// loadAudnexStarts reads a saved Audnexus /chapters response and returns chapter
// start seconds (sorted) + titles.
func loadAudnexStarts(t *testing.T, path string) ([]float64, []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audnex json: %v", err)
	}
	var body struct {
		Chapters []struct {
			StartOffsetMs int    `json:"startOffsetMs"`
			Title         string `json:"title"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("parse audnex json: %v", err)
	}
	type ch struct {
		s float64
		t string
	}
	var chs []ch
	for _, c := range body.Chapters {
		chs = append(chs, ch{float64(c.StartOffsetMs) / 1000, c.Title})
	}
	sort.Slice(chs, func(i, j int) bool { return chs[i].s < chs[j].s })
	starts := make([]float64, len(chs))
	titles := make([]string, len(chs))
	for i, c := range chs {
		starts[i], titles[i] = c.s, c.t
	}
	return starts, titles
}
