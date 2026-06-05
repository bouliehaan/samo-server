package chapteraudio

import "math"

// Gap is one stretch of silence found in a file. We keep every gap above a small
// floor (not just the obvious long ones): the DISTRIBUTION of gap lengths is what
// lets the clusterer tell a chapter break from a dramatic pause, and it needs the
// short pauses present to know what "normal" is for this narrator.
type Gap struct {
	StartSec float64
	EndSec   float64
	Duration float64
	Depth    float64 // mean dB below the silence ceiling; bigger = quieter/cleaner
	MinDB    float64
}

func (g Gap) MidSec() float64 { return (g.StartSec + g.EndSec) / 2 }

// frameSilent decides whether one analysis frame is silence, using BOTH energy
// and spectral shape:
//
//   - At or below the energy gate → silence, period.
//   - A little above the gate → silence ONLY if the frame is spectrally flat
//     (non-tonal). That admits quiet room tone while rejecting a quiet held note,
//     a hum, or a music bed — the things a pure amplitude gate mistakes for an
//     empty gap and then splits a chapter on.
func frameSilent(energy, flatness float64, th thresholds) bool {
	if energy <= th.SilenceDB {
		return true
	}
	if energy <= th.SilenceDB+th.ExtraMarginDB && flatness >= th.FlatGate {
		return true
	}
	return false
}

// findGaps walks the per-frame features and returns every silence run lasting at
// least minGapSeconds.
func findGaps(f Features, th thresholds, minGapSeconds float64) []Gap {
	var gaps []Gap
	n := f.Len()
	ceiling := th.SilenceDB + th.ExtraMarginDB
	i := 0
	for i < n {
		if !frameSilent(f.Energy[i], f.Flatness[i], th) {
			i++
			continue
		}
		j := i
		var sumBelow float64
		minv := math.Inf(1)
		for j < n && frameSilent(f.Energy[j], f.Flatness[j], th) {
			if d := ceiling - f.Energy[j]; d > 0 {
				sumBelow += d
			}
			if f.Energy[j] < minv {
				minv = f.Energy[j]
			}
			j++
		}
		dur := float64(j-i) * HopSeconds
		if dur >= minGapSeconds {
			gaps = append(gaps, Gap{
				StartSec: float64(i) * HopSeconds,
				EndSec:   float64(j) * HopSeconds,
				Duration: dur,
				Depth:    sumBelow / float64(j-i),
				MinDB:    minv,
			})
		}
		i = j
	}
	return gaps
}

// offsetGaps shifts every gap by delta seconds (file-local -> book-global).
func offsetGaps(gaps []Gap, delta float64) []Gap {
	if delta == 0 {
		return gaps
	}
	out := make([]Gap, len(gaps))
	for i, g := range gaps {
		g.StartSec += delta
		g.EndSec += delta
		out[i] = g
	}
	return out
}
