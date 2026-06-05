package chapteraudio

// thresholds are the per-file levels that separate silence from content. Every
// value is derived from the file's OWN distributions — energy AND spectral
// flatness — so there is no fixed "-30 dB" or "tone is silence" assumption. Only
// broad sanity clamps guard a pathological file.
type thresholds struct {
	// SilenceDB: frames at/below this energy are silence outright.
	SilenceDB float64
	// ExtraMarginDB: frames up to SilenceDB+ExtraMarginDB still count as silence
	// IF they are spectrally flat (non-tonal) — i.e. quiet room tone, not a quiet
	// held note. This is the spectral half of the gate.
	ExtraMarginDB float64
	// FlatGate: a frame is "flat/noise-like" when its flatness >= FlatGate. Voiced
	// narration and music sit below it; true silence/room tone above. Adaptive.
	FlatGate float64
	// FloorDB / SpeechDB: representative background and narration energy levels.
	FloorDB  float64
	SpeechDB float64
	// Separation: 0..1 how cleanly energy splits into quiet vs loud (confidence).
	Separation float64
}

const (
	histBins = 100
	histLo   = minDB

	minSilenceDB = minDB + 5
	maxSilenceDB = -20.0

	silenceMarginFrac = 0.20
	silenceMarginMin  = 6.0
	silenceMarginMax  = 15.0
	extraMarginMin    = 6.0
	extraMarginMax    = 12.0

	minDynamicSpan = 10.0
)

// estimateThresholds derives the silence gate from the file's energy
// distribution (floor + adaptive margin) and the tonality gate from its flatness
// distribution (Otsu split between tonal speech and flat silence).
func estimateThresholds(f Features) thresholds {
	if f.Len() == 0 {
		return thresholds{SilenceDB: -45, ExtraMarginDB: 8, FlatGate: 0.4, FloorDB: -60, SpeechDB: -20}
	}

	const binW = -histLo / histBins
	hist := make([]float64, histBins)
	for _, db := range f.Energy {
		idx := int((db - histLo) / binW)
		if idx < 0 {
			idx = 0
		}
		if idx >= histBins {
			idx = histBins - 1
		}
		hist[idx]++
	}
	total := float64(f.Len())

	floorDB := histPercentile(hist, total, 0.05, binW)
	loudDB := histPercentile(hist, total, 0.90, binW)
	separation := otsuEta(hist, total)
	flatGate := adaptiveFlatGate(f.Flatness)

	if loudDB-floorDB < minDynamicSpan {
		return thresholds{
			SilenceDB:     clampF(floorDB-1, minDB, maxSilenceDB),
			ExtraMarginDB: extraMarginMin,
			FlatGate:      flatGate,
			FloorDB:       floorDB,
			SpeechDB:      loudDB,
			Separation:    separation,
		}
	}

	margin := clampF(silenceMarginFrac*(loudDB-floorDB), silenceMarginMin, silenceMarginMax)
	silenceDB := floorDB + margin
	if silenceDB > loudDB-10 {
		silenceDB = loudDB - 10
	}
	silenceDB = clampF(silenceDB, minSilenceDB, maxSilenceDB)

	floorMean, speechMean := classMeans(f.Energy, silenceDB)
	return thresholds{
		SilenceDB:     silenceDB,
		ExtraMarginDB: clampF(margin*0.6, extraMarginMin, extraMarginMax),
		FlatGate:      flatGate,
		FloorDB:       floorMean,
		SpeechDB:      speechMean,
		Separation:    separation,
	}
}

// adaptiveFlatGate finds the flatness value that best separates tonal frames
// (voiced narration / music — low flatness) from flat frames (silence / room
// tone — high flatness) via Otsu on the flatness histogram. Clamped to a sane
// band so a file that's almost all one or the other still gets a usable gate.
func adaptiveFlatGate(flat []float64) float64 {
	const bins = 50
	if len(flat) == 0 {
		return 0.4
	}
	hist := make([]float64, bins)
	for _, v := range flat {
		idx := int(v * bins)
		if idx < 0 {
			idx = 0
		}
		if idx >= bins {
			idx = bins - 1
		}
		hist[idx]++
	}
	total := float64(len(flat))
	var sumAll float64
	for i, c := range hist {
		sumAll += float64(i) * c
	}
	var wB, sumB, best float64
	bestT := -1
	for t := 0; t < bins; t++ {
		wB += hist[t]
		if wB == 0 {
			continue
		}
		wF := total - wB
		if wF == 0 {
			break
		}
		sumB += float64(t) * hist[t]
		mB := sumB / wB
		mF := (sumAll - sumB) / wF
		pB := wB / total
		pF := wF / total
		bw := pB * pF * (mB - mF) * (mB - mF)
		if bw > best {
			best = bw
			bestT = t
		}
	}
	if bestT < 0 {
		return 0.4
	}
	return clampF((float64(bestT)+0.5)/bins, 0.2, 0.85)
}

func histPercentile(hist []float64, total, p, binW float64) float64 {
	target := p * total
	cum := 0.0
	for i, c := range hist {
		cum += c
		if cum >= target {
			return histLo + (float64(i)+0.5)*binW
		}
	}
	return histLo + (float64(len(hist))-0.5)*binW
}

// otsuEta returns Otsu's η (between-class / total variance, 0..1) for the energy
// histogram: how cleanly the file splits into quiet vs loud. Confidence signal.
func otsuEta(hist []float64, total float64) float64 {
	var sumAll float64
	for i, c := range hist {
		sumAll += float64(i) * c
	}
	meanAll := sumAll / total
	var varAll float64
	for i, c := range hist {
		d := float64(i) - meanAll
		varAll += d * d * c
	}
	varAll /= total
	if varAll == 0 {
		return 0
	}
	var wB, sumB, best float64
	for t := 0; t < len(hist); t++ {
		wB += hist[t]
		if wB == 0 {
			continue
		}
		wF := total - wB
		if wF == 0 {
			break
		}
		sumB += float64(t) * hist[t]
		mB := sumB / wB
		mF := (sumAll - sumB) / wF
		pB := wB / total
		pF := wF / total
		between := pB * pF * (mB - mF) * (mB - mF)
		if between > best {
			best = between
		}
	}
	return clampF(best/varAll, 0, 1)
}

// classMeans returns the mean dB of frames below and at/above the gate.
func classMeans(db []float64, gate float64) (below, above float64) {
	var sumB, sumA float64
	var nB, nA int
	for _, v := range db {
		if v < gate {
			sumB += v
			nB++
		} else {
			sumA += v
			nA++
		}
	}
	if nB > 0 {
		below = sumB / float64(nB)
	} else {
		below = gate - 10
	}
	if nA > 0 {
		above = sumA / float64(nA)
	} else {
		above = gate + 10
	}
	return below, above
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
