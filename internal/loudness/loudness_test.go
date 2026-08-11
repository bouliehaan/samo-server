package loudness

import (
	"math"
	"strings"
	"testing"
)

// The policy table. Every row is a real shape of material a mixed station
// actually plays, and the expectations encode the promise the package makes:
// levels get matched, dynamics do not get flattened to do it.
func TestPlan(t *testing.T) {
	cases := []struct {
		name      string
		m         Measurement
		wantGain  float64
		wantLimit bool
		why       string
	}{
		{
			name:      "loudness-war master is turned down and left alone",
			m:         Measurement{IntegratedLUFS: -8, TruePeakDBTP: -0.2},
			wantGain:  -8,
			wantLimit: false,
			why:       "the common case: attenuation always fits under the ceiling, so nothing but a gain is applied",
		},
		{
			name:      "item already on target gets no filter at all",
			m:         Measurement{IntegratedLUFS: -16.2, TruePeakDBTP: -1.5},
			wantGain:  0,
			wantLimit: false,
			why:       "a 0.2 dB correction is inaudible and not worth a filter graph",
		},
		{
			name:      "quiet talk with headroom is lifted cleanly",
			m:         Measurement{IntegratedLUFS: -24, TruePeakDBTP: -9},
			wantGain:  8,
			wantLimit: false,
			why:       "8 dB of lift still lands the peak at -1 dBTP, so the limiter stays out of circuit",
		},
		{
			name:      "dynamic recording is left quiet rather than squashed",
			m:         Measurement{IntegratedLUFS: -26, TruePeakDBTP: -0.5},
			wantGain:  5.5,
			wantLimit: true,
			why:       "reaching -16 would need 10 dB of limiting; the cap stops at 6 and accepts a quieter item",
		},
		{
			name:      "archive recording hits the boost ceiling",
			m:         Measurement{IntegratedLUFS: -35, TruePeakDBTP: -12},
			wantGain:  12,
			wantLimit: true,
			why:       "MaxBoost caps the lift at 12 dB so a noise floor is never promoted to programme",
		},
		{
			name:      "master hotter than full scale is pulled under the ceiling",
			m:         Measurement{IntegratedLUFS: -5, TruePeakDBTP: 1.5},
			wantGain:  -11,
			wantLimit: false,
			why:       "an intersample-clipping master needs attenuation for loudness anyway",
		},
		{
			name:      "digital silence is not amplified",
			m:         Measurement{IntegratedLUFS: -70, TruePeakDBTP: -70},
			wantGain:  0,
			wantLimit: false,
			why:       "boosting silence turns a file's noise floor into the loudest thing on the station",
		},
		{
			name:      "a measurement with no true peak is not a measurement",
			m:         Measurement{IntegratedLUFS: -24, TruePeakDBTP: math.NaN()},
			wantGain:  0,
			wantLimit: false,
			why:       "every pass asks for a peak, so its absence means the pass did not complete",
		},
		{
			name:      "loudness above peak is incoherent and is refused",
			m:         Measurement{IntegratedLUFS: 0, TruePeakDBTP: math.Inf(-1)},
			wantGain:  0,
			wantLimit: false,
			why:       "THE regression: a broken filter printed exactly this, and 0.0 LUFS made the policy cut ~19,000 files by 16 dB",
		},
		{
			name:      "a windowed live measurement is boosted more cautiously",
			m:         Measurement{IntegratedLUFS: -26, TruePeakDBTP: -9, Partial: true},
			wantGain:  6,
			wantLimit: false,
			why:       "45 seconds of a station is not a promise about the rest of it",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := DefaultTarget.Plan(tc.m)
			if math.Abs(plan.GainDB-tc.wantGain) > 0.05 {
				t.Errorf("gain = %+.1f dB, want %+.1f dB\n%s", plan.GainDB, tc.wantGain, tc.why)
			}
			if plan.Limit != tc.wantLimit {
				t.Errorf("limit = %v, want %v\n%s", plan.Limit, tc.wantLimit, tc.why)
			}
		})
	}
}

// The two guarantees the whole design rests on, checked across the entire
// space of plausible material rather than at the handful of points above.
//
// This is the test that answers "will this make everything quiet, or blow
// everything out?" — neither is reachable, for any input.
func TestPlanInvariants(t *testing.T) {
	target := DefaultTarget
	for loudness := -40.0; loudness <= 0; loudness += 0.5 {
		for peak := -40.0; peak <= 3; peak += 0.5 {
			m := Measurement{IntegratedLUFS: loudness, TruePeakDBTP: peak}
			plan := target.Plan(m)
			if !m.Valid() {
				if !plan.Zero() {
					t.Fatalf("unusable measurement %.1f LUFS produced a plan %+v", loudness, plan)
				}
				continue
			}

			// Nothing is ever blown out: the gain can never push peaks more
			// than MaxLimitDB past the ceiling, so the limiter can never be
			// asked to do more than catch transients.
			headroom := target.CeilingDBTP - peak
			over := plan.GainDB - headroom
			if over > target.MaxLimitDB+0.05 {
				t.Fatalf("%.1f LUFS / %.1f dBTP: gain %+.1f asks the limiter for %.1f dB, cap is %.1f",
					loudness, peak, plan.GainDB, over, target.MaxLimitDB)
			}

			// Nothing is ever quietly destroyed either: the limiter is only
			// ever in circuit when the gain genuinely exceeds the headroom.
			if plan.Limit && over <= 0 {
				t.Fatalf("%.1f LUFS / %.1f dBTP: limiter engaged with %.1f dB of headroom to spare",
					loudness, peak, -over)
			}

			// And the boost is bounded regardless of how quiet the source is.
			if plan.GainDB > target.MaxBoostDB+0.05 {
				t.Fatalf("%.1f LUFS: gain %+.1f exceeds the boost cap %.1f",
					loudness, plan.GainDB, target.MaxBoostDB)
			}
			if plan.GainDB < -target.MaxCutDB-0.05 {
				t.Fatalf("%.1f LUFS: gain %+.1f exceeds the cut cap %.1f",
					loudness, plan.GainDB, target.MaxCutDB)
			}
		}
	}
}

// Levelling is only worth anything if items that started far apart end up
// close together. This checks the actual outcome rather than the arithmetic:
// feed the policy a spread of real-world material and measure the spread that
// comes out the other side.
func TestPlanConvergesLevels(t *testing.T) {
	library := []Measurement{
		{IntegratedLUFS: -8.1, TruePeakDBTP: -0.1},  // modern pop master
		{IntegratedLUFS: -11.4, TruePeakDBTP: -0.3}, // rock album
		{IntegratedLUFS: -16.8, TruePeakDBTP: -1.9}, // podcast
		{IntegratedLUFS: -19.2, TruePeakDBTP: -3.4}, // public radio feature
		{IntegratedLUFS: -23.5, TruePeakDBTP: -8.0}, // old radio serial
	}

	lowest, highest := math.Inf(1), math.Inf(-1)
	for _, m := range library {
		plan := DefaultTarget.Plan(m)
		out := m.IntegratedLUFS + plan.GainDB
		lowest = math.Min(lowest, out)
		highest = math.Max(highest, out)
	}

	// Before: 15.4 LU apart, which is the reach-for-the-volume-knob range.
	if spread := highest - lowest; spread > 1 {
		t.Errorf("levelled spread is %.1f LU (%.1f to %.1f LUFS), want everything within 1 LU",
			spread, lowest, highest)
	}
}

func TestFilterSpec(t *testing.T) {
	t.Run("gain only", func(t *testing.T) {
		spec := Plan{GainDB: -6.4}.FilterSpec()
		if spec != "volume=-6.4dB" {
			t.Fatalf("spec = %q", spec)
		}
	})

	t.Run("nothing to do renders nothing", func(t *testing.T) {
		if spec := (Plan{}).FilterSpec(); spec != "" {
			t.Fatalf("spec = %q, want empty so no filter is attached at all", spec)
		}
	})

	t.Run("limiter carries the ceiling and never re-normalises", func(t *testing.T) {
		spec := Plan{GainDB: 5.5, Limit: true, CeilingDBTP: -1}.FilterSpec()
		// -1 dBTP as a linear amplitude.
		if want := "volume=5.5dB,alimiter=limit=0.8913:attack=5:release=60:level=disabled"; spec != want {
			t.Fatalf("spec  = %q\nwant = %q", spec, want)
		}
		// level=disabled is load-bearing: alimiter's default auto-levels its
		// output back up, which would undo the gain decision entirely.
		if !strings.Contains(spec, "level=disabled") {
			t.Fatal("limiter must have level=disabled or it re-normalises the output")
		}
	})
}
