package chapteraudio

import (
	"math"
	"testing"
)

// Prove the FFT is correct before anything depends on it: a pure tone must put
// its energy in exactly one bin, DC must land in bin 0, and flatness must
// separate a tone (peaky) from white noise (flat).

func TestFFTPureToneSingleBin(t *testing.T) {
	const n = 512
	re := make([]float64, n)
	im := make([]float64, n)
	bin := 20
	for i := 0; i < n; i++ {
		re[i] = math.Cos(2 * math.Pi * float64(bin) * float64(i) / float64(n))
	}
	fftRadix2(re, im)
	// Magnitude should peak at `bin` (and its mirror n-bin).
	peak, peakIdx := 0.0, 0
	for k := 0; k <= n/2; k++ {
		m := re[k]*re[k] + im[k]*im[k]
		if m > peak {
			peak, peakIdx = m, k
		}
	}
	if peakIdx != bin {
		t.Fatalf("tone peak at bin %d, want %d", peakIdx, bin)
	}
	// Neighbouring non-peak bins must be tiny relative to the peak.
	other := re[5]*re[5] + im[5]*im[5]
	if other > peak*1e-6 {
		t.Errorf("expected energy concentrated at one bin; bin5=%g peak=%g", other, peak)
	}
}

func TestFFTDC(t *testing.T) {
	const n = 8
	re := []float64{1, 1, 1, 1, 1, 1, 1, 1}
	im := make([]float64, n)
	fftRadix2(re, im)
	if math.Abs(re[0]-8) > 1e-9 || math.Abs(im[0]) > 1e-9 {
		t.Fatalf("DC bin = (%g,%g), want (8,0)", re[0], im[0])
	}
	for k := 1; k < n; k++ {
		if math.Abs(re[k]) > 1e-9 || math.Abs(im[k]) > 1e-9 {
			t.Errorf("bin %d should be ~0, got (%g,%g)", k, re[k], im[k])
		}
	}
}

func TestSpectralFlatnessToneVsNoise(t *testing.T) {
	const n = 512
	sp := newSpectrum(n)

	tone := make([]float64, n)
	for i := 0; i < n; i++ {
		tone[i] = math.Sin(2 * math.Pi * 40 * float64(i) / float64(n))
	}
	toneFlat := spectralFlatness(sp.power(tone), 1, n/2)

	// Deterministic pseudo-noise.
	noise := make([]float64, n)
	x := uint32(12345)
	for i := 0; i < n; i++ {
		x = x*1664525 + 1013904223
		noise[i] = (float64(x>>8)/float64(1<<24))*2 - 1
	}
	noiseFlat := spectralFlatness(sp.power(noise), 1, n/2)

	if toneFlat > 0.1 {
		t.Errorf("tone flatness %.3f should be near 0 (peaky)", toneFlat)
	}
	if noiseFlat < 0.2 {
		t.Errorf("noise flatness %.3f should be clearly higher (flat)", noiseFlat)
	}
	if !(noiseFlat > toneFlat*5) {
		t.Errorf("noise (%.3f) should be far flatter than tone (%.3f)", noiseFlat, toneFlat)
	}
}
