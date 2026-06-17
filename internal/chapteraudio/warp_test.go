package chapteraudio

import (
	"math"
	"testing"
)

// leadingSilence must read a head silence as the content onset, ignore a file
// that opens straight into speech, and refuse to treat a near-silent or
// pathologically long head gap as drift.
func TestLeadingSilence(t *testing.T) {
	cases := []struct {
		name string
		gaps []Gap
		dur  float64
		want float64
	}{
		{"opens with content", []Gap{{StartSec: 12, EndSec: 13, Duration: 1}}, 300, 0},
		{"head silence is the onset", []Gap{{StartSec: 0, EndSec: 0.4, Duration: 0.4}}, 300, 0.4},
		{"head silence within a frame", []Gap{{StartSec: HopSeconds, EndSec: 0.5, Duration: 0.5 - HopSeconds}}, 300, 0.5},
		{"no gaps", nil, 300, 0},
		{"all-silent file is not drift", []Gap{{StartSec: 0, EndSec: 280, Duration: 280}}, 300, 0},
		{"caps an implausibly long head gap", []Gap{{StartSec: 0, EndSec: 40, Duration: 40}}, 300, 8.0},
	}
	for _, tc := range cases {
		if got := leadingSilence(tc.gaps, tc.dur); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: leadingSilence=%.4f, want %.4f", tc.name, got, tc.want)
		}
	}
}

// The drift model must turn per-file onsets into the cumulative drift and the
// de-drifted content clock, and deDrift/reDrift must be exact inverses on content
// positions — the property the whole Phase-2 transform rests on.
func TestDriftModelRoundTrip(t *testing.T) {
	files := []FileAnalysis{
		{DurationSec: 10.0, SpeechOnsetSec: 0.0},
		{DurationSec: 10.5, SpeechOnsetSec: 0.5},
		{DurationSec: 10.5, SpeechOnsetSec: 0.5},
	}
	dm := buildDriftModel(files)

	if !dm.active() {
		t.Fatal("model with measured onsets must be active")
	}
	if math.Abs(dm.total-1.0) > 1e-9 {
		t.Fatalf("total drift = %.4f, want 1.0", dm.total)
	}
	wantMaster := []float64{0, 10, 20} // content starts on the de-drifted clock
	for k, w := range wantMaster {
		if math.Abs(dm.master[k]-w) > 1e-9 {
			t.Errorf("masterContentStart[%d] = %.4f, want %.4f", k, dm.master[k], w)
		}
	}

	// Each file's content start (file time) de-drifts to its master content start.
	for k := range files {
		fileContentStart := dm.fileStart[k] + files[k].SpeechOnsetSec
		if got := dm.deDrift(fileContentStart); math.Abs(got-dm.master[k]) > 1e-9 {
			t.Errorf("deDrift(file %d content start %.3f) = %.4f, want %.4f", k, fileContentStart, got, dm.master[k])
		}
	}

	// Round-trip a spread of content positions.
	for _, fileT := range []float64{0.0, 5.0, 10.5, 18.0, 25.0, 30.0} {
		if got := dm.reDrift(dm.deDrift(fileT)); math.Abs(got-fileT) > 1e-9 {
			t.Errorf("reDrift(deDrift(%.3f)) = %.4f, not a round-trip", fileT, got)
		}
	}

	// A single file (or no onsets) is inactive and the identity.
	single := buildDriftModel([]FileAnalysis{{DurationSec: 3600, SpeechOnsetSec: 2}})
	if single.active() {
		t.Fatal("single-file model must be inactive")
	}
	if got := single.deDrift(123.4); got != 123.4 {
		t.Fatalf("inactive deDrift must be identity, got %.4f", got)
	}
}
