package chapteraudio

import "math"

// A small, dependency-free radix-2 FFT. We use it to read each analysis window
// the way you'd read a spectrogram column: not just "how loud" but "what SHAPE"
// — is the energy spread flat across the band (room tone / true silence) or
// concentrated in harmonics (a voiced narrator)? That spectral distinction is
// what lets the detector tell a genuine chapter gap from a quiet held note or a
// music bed, instead of trusting amplitude alone.

// fftRadix2 computes the in-place iterative radix-2 FFT of re/im. len(re) must be
// a power of two and equal to len(im).
func fftRadix2(re, im []float64) {
	n := len(re)
	if n <= 1 {
		return
	}
	// Decimation-in-time bit-reversal permutation.
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		ang := -2 * math.Pi / float64(length)
		wlenRe, wlenIm := math.Cos(ang), math.Sin(ang)
		half := length >> 1
		for i := 0; i < n; i += length {
			wRe, wIm := 1.0, 0.0
			for k := 0; k < half; k++ {
				a := i + k
				b := a + half
				vRe := re[b]*wRe - im[b]*wIm
				vIm := re[b]*wIm + im[b]*wRe
				re[b] = re[a] - vRe
				im[b] = im[a] - vIm
				re[a] += vRe
				im[a] += vIm
				wRe, wIm = wRe*wlenRe-wIm*wlenIm, wRe*wlenIm+wIm*wlenRe
			}
		}
	}
}

// hannWindow returns an n-point periodic Hann window.
func hannWindow(n int) []float64 {
	w := make([]float64, n)
	if n == 1 {
		w[0] = 1
		return w
	}
	for i := 0; i < n; i++ {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
	}
	return w
}

// spectrum is reusable scratch for windowed power-spectrum computation so the
// per-hop hot loop allocates nothing.
type spectrum struct {
	n    int
	hann []float64
	re   []float64
	im   []float64
	pow  []float64 // n/2+1 one-sided power bins
}

func newSpectrum(n int) *spectrum {
	return &spectrum{
		n:    n,
		hann: hannWindow(n),
		re:   make([]float64, n),
		im:   make([]float64, n),
		pow:  make([]float64, n/2+1),
	}
}

// power computes the one-sided power spectrum of a Hann-windowed frame. samples
// must have length s.n. Returns the internal pow slice (valid until the next call).
func (s *spectrum) power(samples []float64) []float64 {
	for i := 0; i < s.n; i++ {
		s.re[i] = samples[i] * s.hann[i]
		s.im[i] = 0
	}
	fftRadix2(s.re, s.im)
	for k := 0; k <= s.n/2; k++ {
		s.pow[k] = s.re[k]*s.re[k] + s.im[k]*s.im[k]
	}
	return s.pow
}

// spectralFlatness is the ratio of geometric to arithmetic mean of power across
// bins [loBin, hiBin]. ~1.0 means flat/noise-like (room tone, broadband hiss —
// what a real silence looks like spectrally); near 0 means tonal/peaky (a voiced
// vowel, a hum, a music note). This is the core "is it actually empty" signal.
func spectralFlatness(pow []float64, loBin, hiBin int) float64 {
	if loBin < 1 {
		loBin = 1
	}
	if hiBin >= len(pow) {
		hiBin = len(pow) - 1
	}
	if hiBin < loBin {
		return 1
	}
	const eps = 1e-12
	var logSum, sum float64
	count := 0
	for k := loBin; k <= hiBin; k++ {
		p := pow[k] + eps
		logSum += math.Log(p)
		sum += p
		count++
	}
	if count == 0 || sum <= 0 {
		return 1
	}
	geo := math.Exp(logSum / float64(count))
	arith := sum / float64(count)
	return clampF(geo/arith, 0, 1)
}
