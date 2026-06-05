package chapteraudio

import "math"

const (
	// fftSize is the analysis window: 512 samples = 32 ms at 16 kHz. A power of
	// two for the radix-2 FFT, fine enough to place a chapter boundary within a
	// frame, coarse enough that a 12-hour book is ~1.3M frames.
	fftSize = 512

	minDB      = -100.0 // silence clamp so log10(0) never yields -Inf
	rmsEpsilon = 1e-5   // 10^(minDB/20)

	// Speech-band bin range for spectral flatness (binWidth = 16000/512 = 31.25
	// Hz): ~100 Hz to ~7 kHz, where narration lives.
	flatLoBin = 3   // ~94 Hz
	flatHiBin = 224 // ~7 kHz
)

// HopSeconds is the wall-clock duration of one analysis frame.
const HopSeconds = float64(fftSize) / float64(SampleRate)

// Features is the per-frame description of one decoded file. For each 32 ms frame
// we keep two numbers: how loud it is, and how spectrally FLAT it is. Energy
// alone can't tell a chapter gap from a quiet held note; flatness can — true
// silence/room tone is broadband-flat, voiced narration and music are peaky.
type Features struct {
	Energy   []float64 // per-frame loudness, dBFS (RMS), clamped at minDB
	Flatness []float64 // per-frame spectral flatness in [0,1]; high = flat/noise-like
}

func (f Features) Len() int { return len(f.Energy) }

func (f Features) DurationSeconds() float64 { return float64(len(f.Energy)) * HopSeconds }

// featureBuilder folds a streaming PCM signal into per-frame energy + flatness.
// Feed sample chunks with add(); call finish() once at the end.
type featureBuilder struct {
	sp       *spectrum
	buf      []float64
	pos      int
	energy   []float64
	flatness []float64
}

func newFeatureBuilder(expectedSeconds float64) *featureBuilder {
	capFrames := 0
	if expectedSeconds > 0 {
		capFrames = int(expectedSeconds/HopSeconds) + 1
	}
	return &featureBuilder{
		sp:       newSpectrum(fftSize),
		buf:      make([]float64, fftSize),
		energy:   make([]float64, 0, capFrames),
		flatness: make([]float64, 0, capFrames),
	}
}

func (b *featureBuilder) add(samples []float32) {
	for _, s := range samples {
		b.buf[b.pos] = float64(s)
		b.pos++
		if b.pos == fftSize {
			b.emit(fftSize)
			b.pos = 0
		}
	}
}

func (b *featureBuilder) emit(n int) {
	var sumSq float64
	for i := 0; i < n; i++ {
		sumSq += b.buf[i] * b.buf[i]
	}
	rms := math.Sqrt(sumSq / float64(n))
	if rms < rmsEpsilon {
		rms = rmsEpsilon
	}
	db := 20 * math.Log10(rms)
	if db < minDB {
		db = minDB
	}
	pow := b.sp.power(b.buf)
	b.energy = append(b.energy, db)
	b.flatness = append(b.flatness, spectralFlatness(pow, flatLoBin, flatHiBin))
}

func (b *featureBuilder) finish() Features {
	// Process a final partial frame (>= half a window) zero-padded, so we don't
	// lose up to 32 ms of audio at the very end.
	if b.pos >= fftSize/2 {
		for i := b.pos; i < fftSize; i++ {
			b.buf[i] = 0
		}
		b.emit(b.pos)
	}
	return Features{Energy: b.energy, Flatness: b.flatness}
}

// computeFeatures builds Features from in-memory samples (tests / whole-signal
// callers).
func computeFeatures(samples []float32) Features {
	b := newFeatureBuilder(float64(len(samples)) / float64(SampleRate))
	b.add(samples)
	return b.finish()
}
