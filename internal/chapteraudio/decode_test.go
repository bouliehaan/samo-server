package chapteraudio

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// These tests run the WHOLE pipeline through a real ffmpeg binary (decode +
// speech-band bandpass + f32le streaming), not just the in-memory math. They
// generate WAV fixtures with a known chapter structure and assert the analyzer
// recovers it. Skipped automatically where ffmpeg isn't installed.

func ffmpegOrSkip(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH; skipping end-to-end decode test")
	}
	return p
}

func TestAnalyzeBookEndToEndSingleFile(t *testing.T) {
	ff := ffmpegOrSkip(t)
	signal, want := synthBook(4, 2.5)
	dir := t.TempDir()
	path := filepath.Join(dir, "book.wav")
	writeWAV16(t, path, signal)

	a := NewAnalyzer(ff)
	rep, err := a.AnalyzeBook(context.Background(), []FileInput{{Path: path, DurationSec: float64(len(signal)) / SampleRate}}, nil)
	if err != nil {
		t.Fatalf("AnalyzeBook: %v", err)
	}
	if got := len(rep.Boundaries); got != len(want) {
		t.Fatalf("boundaries: got %d %v, want %d %v (split=%.2f conf=%.2f)",
			got, rounded(rep.Boundaries), len(want), rounded(want), rep.SplitSeconds, rep.Confidence)
	}
	for i, b := range rep.Boundaries {
		if math.Abs(b-want[i]) > 0.6 {
			t.Errorf("boundary %d at %.2fs, want ~%.2fs", i, b, want[i])
		}
	}
	if rep.AudioCount != len(want)+1 {
		t.Errorf("AudioCount=%d, want %d", rep.AudioCount, len(want)+1)
	}
	if rep.Recommendation != RecommendApply {
		t.Errorf("recommendation=%q, want %q (conf=%.2f)", rep.Recommendation, RecommendApply, rep.Confidence)
	}
	// Audio-only: chapters should be generic since no metadata was supplied.
	for _, c := range rep.Chapters {
		if c.Named {
			t.Errorf("chapter %d unexpectedly named %q with no metadata", c.Index, c.Title)
		}
	}
}

func TestAnalyzeBookEndToEndMultiFileUsesFileSeams(t *testing.T) {
	ff := ffmpegOrSkip(t)
	dir := t.TempDir()

	// Two files, each one continuous "chapter" (no internal chapter pauses). The
	// file seam alone must yield exactly two chapters.
	var files []FileInput
	for i := 0; i < 2; i++ {
		sig, _ := synthBook(1, 2.5) // single chapter, no long pauses
		p := filepath.Join(dir, "part"+string(rune('1'+i))+".wav")
		writeWAV16(t, p, sig)
		files = append(files, FileInput{Path: p, DurationSec: float64(len(sig)) / SampleRate})
	}

	a := NewAnalyzer(ff)
	rep, err := a.AnalyzeBook(context.Background(), files, nil)
	if err != nil {
		t.Fatalf("AnalyzeBook: %v", err)
	}
	if rep.AudioCount != 2 {
		t.Fatalf("AudioCount=%d, want 2 (boundaries=%v)", rep.AudioCount, rounded(rep.Boundaries))
	}
	if rep.FileBoundaryCount != 1 {
		t.Errorf("FileBoundaryCount=%d, want 1", rep.FileBoundaryCount)
	}
	if rep.Recommendation != RecommendApply {
		t.Errorf("recommendation=%q want apply (conf=%.2f)", rep.Recommendation, rep.Confidence)
	}
}

// writeWAV16 writes mono 16-bit PCM WAV at the analysis sample rate.
func writeWAV16(t *testing.T, path string, samples []float32) {
	t.Helper()
	var buf bytes.Buffer
	le := binary.LittleEndian
	dataLen := len(samples) * 2
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, le, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, le, uint32(16))
	_ = binary.Write(&buf, le, uint16(1)) // PCM
	_ = binary.Write(&buf, le, uint16(1)) // mono
	_ = binary.Write(&buf, le, uint32(SampleRate))
	_ = binary.Write(&buf, le, uint32(SampleRate*2)) // byte rate
	_ = binary.Write(&buf, le, uint16(2))            // block align
	_ = binary.Write(&buf, le, uint16(16))           // bits/sample
	buf.WriteString("data")
	_ = binary.Write(&buf, le, uint32(dataLen))
	for _, s := range samples {
		v := int32(float64(s) * 32767)
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		_ = binary.Write(&buf, le, int16(v))
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
}

func rounded(v []float64) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = math.Round(x*100) / 100
	}
	return out
}
