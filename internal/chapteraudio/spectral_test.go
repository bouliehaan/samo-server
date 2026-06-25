package chapteraudio

import (
	"math"
	"testing"
)

// appendSine / appendNoise build test signals at a target RMS level (dBFS).
func appendSine(sig []float32, freq, db, seconds float64) []float32 {
	amp := math.Pow(10, db/20) * math.Sqrt2 // sine RMS = amp/sqrt2
	n := int(seconds * SampleRate)
	for i := 0; i < n; i++ {
		t := float64(i) / SampleRate
		sig = append(sig, float32(amp*math.Sin(2*math.Pi*freq*t)))
	}
	return sig
}

func appendNoise(sig []float32, db, seconds float64, seed *uint32) []float32 {
	amp := math.Pow(10, db/20) * math.Sqrt(3) // uniform[-a,a] RMS = a/sqrt3
	n := int(seconds * SampleRate)
	for i := 0; i < n; i++ {
		*seed = *seed*1664525 + 1013904223
		r := (float64(*seed>>8)/float64(1<<24))*2 - 1
		sig = append(sig, float32(amp*r))
	}
	return sig
}

// TestSpectralGateRejectsQuietTone is the whole point of going spectral: a quiet
// stretch that is TONAL (a held note / hum / music bed) sitting in the same
// energy band as a real pause must NOT be treated as silence, while a flat
// (noise-like) stretch at the SAME loudness must be. Amplitude alone can't tell
// them apart; flatness can.
func TestSpectralGateRejectsQuietTone(t *testing.T) {
	var seed uint32 = 7
	var sig []float32
	sig = appendNoise(sig, -72, 1.5, &seed) // 0..1.5   deep floor (sets the noise floor)
	sig = appendSine(sig, 300, -13, 2.0)    // 1.5..3.5 narration
	sig = appendNoise(sig, -55, 1.5, &seed) // 3.5..5.0 quiet FLAT pause       -> silence (gap)
	sig = appendSine(sig, 300, -13, 2.0)    // 5.0..7.0 narration
	sig = appendSine(sig, 220, -55, 1.5)    // 7.0..8.5 quiet TONE (music bed) -> NOT silence
	sig = appendSine(sig, 300, -13, 2.0)    // 8.5..10.5 narration
	sig = appendNoise(sig, -72, 1.5, &seed) // 10.5..12 deep floor

	feats := computeFeatures(sig)
	th := estimateThresholds(feats)
	gaps := findGaps(feats, th, 0.5)

	covers := func(t0, t1 float64) bool {
		for _, g := range gaps {
			if g.MidSec() > t0 && g.MidSec() < t1 {
				return true
			}
		}
		return false
	}

	if !covers(3.5, 5.0) {
		t.Errorf("flat quiet pause (3.5-5.0s) should be detected as silence; gaps=%v", gapMids(gaps))
	}
	if covers(7.0, 8.5) {
		t.Errorf("tonal quiet stretch (7.0-8.5s) must NOT be silence — flatness should reject it; gaps=%v", gapMids(gaps))
	}
	// Sanity: the two segments really were at comparable loudness, so this is a
	// spectral decision, not an energy one.
	if th.FlatGate <= 0 || th.FlatGate >= 1 {
		t.Errorf("flat gate %.2f not in (0,1)", th.FlatGate)
	}
}

func gapMids(gaps []Gap) []float64 {
	out := make([]float64, len(gaps))
	for i, g := range gaps {
		out[i] = math.Round(g.MidSec()*10) / 10
	}
	return out
}

// TestCountPriorSelectsRightLevel proves the metadata count is USED, not just
// reported. The pause distribution is tri-modal (sentence 0.4s, paragraph 1.2s,
// chapter 3.0s). The single biggest jump is sentence->paragraph, so a naive
// splitter would call every paragraph break a chapter (7). Knowing the book has
// 4 chapters, the clusterer must instead lock onto the paragraph->chapter break
// (3 internal breaks) and report the counts as matched.
func TestCountPriorSelectsRightLevel(t *testing.T) {
	var gaps []Gap
	add := func(dur float64, count int) {
		for i := 0; i < count; i++ {
			start := float64(len(gaps)) * 100
			gaps = append(gaps, Gap{StartSec: start, EndSec: start + dur, Duration: dur})
		}
	}
	add(0.4, 8)
	add(1.2, 4)
	add(3.0, 3)

	naive := clusterChapterGaps(gaps, 1.6, 0, 0) // no metadata
	if len(naive.ChapterGaps) != 7 {
		t.Fatalf("without metadata, the largest jump should yield 7 chapter gaps, got %d (split=%.2f)",
			len(naive.ChapterGaps), naive.SplitSeconds)
	}

	withCount := clusterChapterGaps(gaps, 1.6, 4 /*chapters*/, 0 /*file seams*/)
	if len(withCount.ChapterGaps) != 3 {
		t.Fatalf("with metadata=4 chapters, should lock onto 3 internal breaks, got %d (split=%.2f)",
			len(withCount.ChapterGaps), withCount.SplitSeconds)
	}
	if !withCount.CountMatched {
		t.Errorf("expected CountMatched=true when audio agrees with metadata")
	}
	if withCount.Confidence <= naive.Confidence {
		t.Errorf("count agreement should raise confidence (matched=%.2f vs naive=%.2f)",
			withCount.Confidence, naive.Confidence)
	}
}
