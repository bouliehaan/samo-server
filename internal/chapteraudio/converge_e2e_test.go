package chapteraudio

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The count-driven path, end to end through real ffmpeg: a 5-chapter book whose
// authoritative metadata supplies the count + names (with the timestamps
// deliberately drifted). The detector must converge to exactly 5 chapters, place
// each boundary at the START of the chapter-break silence (not its midpoint), and
// label them in order from the metadata.
func TestAnalyzeBookHardTargetConvergesAtSilenceStart(t *testing.T) {
	ff := ffmpegOrSkip(t)
	const longPause = 1.2
	signal, want := synthBook(5, longPause) // 5 chapters -> 4 internal boundaries
	dir := t.TempDir()
	path := filepath.Join(dir, "book.wav")
	writeWAV16(t, path, signal)

	// Chapter one at 0; the rest at the silence start (mid - longPause/2), perturbed
	// by +0.3s so the position prior is exercised rather than handed exact answers.
	meta := []catalog.AudioChapter{{Index: 1, Title: "Opening", StartSeconds: 0}}
	titles := []string{"The Road", "The City", "The Tower", "The End"}
	for i, mid := range want {
		meta = append(meta, catalog.AudioChapter{
			Index:        i + 2,
			Title:        titles[i],
			StartSeconds: mid - longPause/2 + 0.3,
		})
	}

	a := NewAnalyzer(ff)
	a.Params.HardTargetCount = true
	a.Params.BoundaryAtSilenceStart = true

	rep, err := a.AnalyzeBook(context.Background(),
		[]FileInput{{Path: path, DurationSec: float64(len(signal)) / SampleRate}}, meta)
	if err != nil {
		t.Fatalf("AnalyzeBook: %v", err)
	}

	if !rep.HardTarget || rep.TargetCount != 5 {
		t.Fatalf("HardTarget=%v TargetCount=%d, want true/5", rep.HardTarget, rep.TargetCount)
	}
	if rep.AudioCount != 5 || !rep.CountMatched {
		t.Fatalf("AudioCount=%d matched=%v, want exactly 5 (boundaries=%v gate=+%.0f)",
			rep.AudioCount, rep.CountMatched, rounded(rep.Boundaries), rep.GateOffsetDB)
	}
	if rep.Recommendation != RecommendApply {
		t.Fatalf("recommendation=%q want apply (conf=%.2f)", rep.Recommendation, rep.Confidence)
	}

	// Each boundary sits at the START of its long pause, ~longPause/2 BEFORE the
	// midpoint the soft path would have used.
	for i, b := range rep.Boundaries {
		start := want[i] - longPause/2
		if math.Abs(b-start) > 0.35 {
			t.Errorf("boundary %d at %.2fs, want ~%.2fs (silence start, not midpoint %.2fs)", i, b, start, want[i])
		}
	}

	wantTitles := []string{"Opening", "The Road", "The City", "The Tower", "The End"}
	for i, c := range rep.Chapters {
		if !c.Named || c.Title != wantTitles[i] {
			t.Errorf("chapter %d: title=%q named=%v, want %q named", i+1, c.Title, c.Named, wantTitles[i])
		}
	}
}

// CD-rip end to end: a book split into 6 arbitrary track files (seams do NOT
// fall on chapter boundaries except one) whose metadata says 3 chapters. The
// detector must converge to exactly 3 — picking the aligned seam and the real
// mid-track chapter silences — instead of emitting one chapter per file, which
// is the bug that turned a ~60-chapter book into 150 "chapters".
func TestAnalyzeBookCDRipConvergesToMetadataCount(t *testing.T) {
	ff := ffmpegOrSkip(t)
	const longPause = 2.5
	// One continuous 3-chapter signal, then slice it into 6 files of ~equal size.
	signal, mids := synthBook(3, longPause)
	dir := t.TempDir()

	per := len(signal)/6 + 1
	var files []FileInput
	for i := 0; i < 6; i++ {
		lo := i * per
		hi := lo + per
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
		meta = append(meta, catalog.AudioChapter{
			Index: i + 2, Title: fmt.Sprintf("Ch %d", i+2), StartSeconds: mid - longPause/2,
		})
	}

	a := NewAnalyzer(ff)
	a.Params.HardTargetCount = true
	a.Params.BoundaryAtSilenceStart = true

	rep, err := a.AnalyzeBook(context.Background(), files, meta)
	if err != nil {
		t.Fatalf("AnalyzeBook: %v", err)
	}
	if rep.AudioCount != 3 || !rep.CountMatched {
		t.Fatalf("AudioCount=%d matched=%v (boundaries=%v files=%d), want exactly 3 chapters",
			rep.AudioCount, rep.CountMatched, rounded(rep.Boundaries), len(files))
	}
	for i, b := range rep.Boundaries {
		start := mids[i] - longPause/2
		if math.Abs(b-start) > 1.0 {
			t.Errorf("boundary %d at %.2fs, want ~%.2fs", i, b, start)
		}
	}
	if rep.Recommendation != RecommendApply {
		t.Errorf("recommendation=%q want apply (conf=%.2f)", rep.Recommendation, rep.Confidence)
	}
}
