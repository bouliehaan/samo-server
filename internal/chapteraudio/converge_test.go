package chapteraudio

import (
	"math"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func mkGap(start, dur, depth float64) Gap {
	return Gap{StartSec: start, EndSec: start + dur, Duration: dur, Depth: depth}
}

func startParams() Params {
	p := DefaultParams()
	p.BoundaryAtSilenceStart = true
	return p
}

func chosenTimes(cs []chapterCandidate) []float64 {
	out := make([]float64, len(cs))
	for i, c := range cs {
		out[i] = c.Time
	}
	return out
}

// THE Eragon regression: a CD rip whose track seams vastly outnumber the real
// chapters. 9 seams but the metadata says 4 chapters — convergence must keep
// exactly the 3 seams that sit where the metadata expects chapters and discard
// the other 6, instead of treating every seam as a chapter (the bug that turned
// a ~60-chapter book into 150 "chapters").
func TestCDRipConvergesToMetadataCountNotFileCount(t *testing.T) {
	p := startParams()
	// Track seams every ~360 s; true chapters start at seams 1080, 2160, 2880.
	fileStarts := []float64{360, 720, 1080, 1440, 1800, 2160, 2520, 2880, 3240}
	expected := []float64{1085, 2150, 2885} // metadata positions, slightly drifted
	rungs := []ladderRung{{gateOffsetDB: 0, gaps: nil}}

	res := convergeBoundaries(rungs, fileStarts, expected, 3, p, 3600, p.PositionDriftToleranceSec)
	if !res.matched {
		t.Fatalf("expected convergence, got %+v", res)
	}
	got := chosenTimes(res.chosen)
	want := []float64{1080, 2160, 2880}
	if len(got) != len(want) {
		t.Fatalf("chose %v, want %v", got, want)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("chose %v, want %v (the seams at expected chapter positions)", got, want)
		}
	}
}

// A chapter that starts MID-track (CD slicing doesn't respect chapters): the
// silence inside the track must beat the off-position seams.
func TestMidTrackChapterSilenceBeatsFarSeam(t *testing.T) {
	p := startParams()
	fileStarts := []float64{360, 720} // arbitrary track edges
	expected := []float64{520}        // metadata: one internal chapter at ~520
	rungs := []ladderRung{{gateOffsetDB: 0, gaps: []Gap{mkGap(522, 2.4, 20)}}}

	res := convergeBoundaries(rungs, fileStarts, expected, 1, p, 1080, p.PositionDriftToleranceSec)
	if !res.matched || len(res.chosen) != 1 {
		t.Fatalf("expected one boundary, got %+v", res)
	}
	if got := res.chosen[0]; got.FromFile || math.Abs(got.Time-522) > 1e-9 {
		t.Fatalf("chose %+v, want the silence start at 522, not a seam", got)
	}
}

// A chapter-per-file book: every seam aligns with an expected position, so all
// seams are kept — the well-behaved multi-file case keeps working exactly as it
// does today.
func TestChapterPerFileKeepsAllSeams(t *testing.T) {
	p := startParams()
	fileStarts := []float64{600, 1230, 1790}
	expected := []float64{598, 1228, 1795}
	rungs := []ladderRung{{gateOffsetDB: 0, gaps: nil}}

	res := convergeBoundaries(rungs, fileStarts, expected, 3, p, 2400, p.PositionDriftToleranceSec)
	if !res.matched {
		t.Fatalf("expected convergence: %+v", res)
	}
	got := chosenTimes(res.chosen)
	for i, want := range fileStarts {
		if math.Abs(got[i]-want) > 1e-9 {
			t.Fatalf("boundary %d at %v, want seam %v", i, got[i], want)
		}
	}
	for _, c := range res.chosen {
		if !c.supported {
			t.Fatalf("on-position seam should be supported: %+v", c)
		}
	}
}

// A dramatic in-chapter pause LONGER than a real chapter break must not be
// chosen when the metadata says roughly where the chapters are.
func TestPositionPriorRejectsDramaticPause(t *testing.T) {
	p := startParams()
	expected := []float64{590, 1190}
	rungs := []ladderRung{{gateOffsetDB: 0, gaps: []Gap{
		mkGap(590, 2.0, 20),  // true chapter 2 start
		mkGap(1190, 2.2, 20), // true chapter 3 start
		mkGap(300, 2.6, 20),  // longest, but planted mid-chapter 1
	}}}

	res := convergeBoundaries(rungs, nil, expected, 2, p, 1800, p.PositionDriftToleranceSec)
	got := chosenTimes(res.chosen)
	if len(got) != 2 || math.Abs(got[0]-590) > 1 || math.Abs(got[1]-1190) > 1 {
		t.Fatalf("selected %v, want [590 1190] — dramatic pause at 300 should be rejected", got)
	}
}

// Drift within the tolerance window is absorbed: a real break 60s off its
// metadata position still matches, and a break beyond tolerance does not get
// force-fitted (the convergence honestly fails instead).
func TestAssignmentToleratesDriftButNotBeyond(t *testing.T) {
	p := startParams()
	rungs := []ladderRung{{gateOffsetDB: 0, gaps: []Gap{mkGap(660, 2.4, 20)}}}
	res := convergeBoundaries(rungs, nil, []float64{600}, 1, p, 1200, p.PositionDriftToleranceSec)
	if got := chosenTimes(res.chosen); !res.matched || len(got) != 1 || math.Abs(got[0]-660) > 1 {
		t.Fatalf("selected %v (matched=%v), want [660] — drifted real break should survive", got, res.matched)
	}

	far := convergeBoundaries(rungs, nil, []float64{200}, 1, p, 1200, p.PositionDriftToleranceSec)
	if far.matched {
		t.Fatalf("a break 460s from the only expected position must not be force-fitted")
	}
}

// When the file's own floor surfaces too few silences, the search climbs the
// gate ladder until enough appear, and reports how far it had to loosen.
func TestConvergeLoosensGateToReachCount(t *testing.T) {
	p := startParams()
	rungs := []ladderRung{
		{gateOffsetDB: 0, gaps: []Gap{mkGap(500, 2.0, 20)}},
		{gateOffsetDB: 2, gaps: []Gap{mkGap(500, 2.0, 20), mkGap(1000, 1.5, 15), mkGap(1500, 1.4, 15)}},
		{gateOffsetDB: 4, gaps: nil},
	}
	res := convergeBoundaries(rungs, nil, nil, 3, p, 2000, p.PositionDriftToleranceSec)
	if !res.matched {
		t.Fatalf("expected matched at +2 dB, got %+v", res)
	}
	if res.gateOffsetDB != 2 {
		t.Fatalf("converged at gate +%.0f dB, want +2", res.gateOffsetDB)
	}
	if len(res.chosen) != 3 {
		t.Fatalf("boundaries=%v, want 3", chosenTimes(res.chosen))
	}
	if res.note == "" {
		t.Fatalf("expected a note recording the gate loosening")
	}
}

// A continuous / music-bed recording that cannot yield the demanded number of
// boundaries degrades: best effort, matched=false, and a note saying so.
func TestConvergeDegradesWhenUnreachable(t *testing.T) {
	p := startParams()
	rungs := []ladderRung{
		{gateOffsetDB: 0, gaps: []Gap{mkGap(500, 2.0, 20)}},
		{gateOffsetDB: 2, gaps: []Gap{mkGap(500, 2.0, 20), mkGap(1000, 1.5, 15)}},
	}
	res := convergeBoundaries(rungs, nil, nil, 5, p, 2000, p.PositionDriftToleranceSec)
	if res.matched {
		t.Fatalf("need=5 with at most 2 silences should not match")
	}
	if len(res.chosen) != 2 {
		t.Fatalf("best effort should keep the 2 found, got %v", chosenTimes(res.chosen))
	}
	if res.note == "" {
		t.Fatalf("expected a degrade note")
	}
}

// The no-positions fallback must respect mutual spacing so the merge step can
// never collapse two picks and silently change the count: with two long gaps 1s
// apart and need=2, the second pick must come from elsewhere.
func TestTopCandidatesEnforcesSpacing(t *testing.T) {
	pool := []chapterCandidate{
		{Time: 500.0, score: 3},
		{Time: 501.0, score: 2.9}, // within MergeWithin of the winner — suppressed
		{Time: 900.0, score: 1},
	}
	chosen := topCandidates(pool, 2, 1.5)
	if len(chosen) != 2 || chosen[0].Time != 500 || chosen[1].Time != 900 {
		t.Fatalf("chose %v, want [500 900]", chosenTimes(chosen))
	}
}

func TestValidCandidatesDropsEdgesAndSeams(t *testing.T) {
	p := startParams() // edge = max(1.5, 2) = 2; seam window = 1.5
	gaps := []Gap{
		mkGap(1.0, 2, 20),   // within 2s of start -> drop
		mkGap(300, 2, 20),   // keep
		mkGap(500.5, 2, 20), // within 1.5s of seam 500 -> drop
		mkGap(700, 2, 20),   // keep
		mkGap(999.0, 2, 20), // within 2s of end (total 1000) -> drop
	}
	valid := validCandidates(gaps, []float64{500}, p, 1000)
	if len(valid) != 2 || math.Abs(valid[0].StartSec-300) > 1e-9 || math.Abs(valid[1].StartSec-700) > 1e-9 {
		t.Fatalf("valid=%v, want starts [300 700]", valid)
	}
}

// The adaptive drift tolerance: when the metadata's runtime disagrees with the
// files by more than the base tolerance, expected positions are allowed that
// much slack instead of penalising every candidate for the edition gap.
func TestHardTargetWidensToleranceToRuntimeDelta(t *testing.T) {
	meta := []catalog.AudioChapter{
		{Index: 1, Title: "One", StartSeconds: 0, EndSeconds: 600},
		{Index: 2, Title: "Two", StartSeconds: 600, EndSeconds: 1900}, // meta runtime 1900
	}
	// Files run 1600s — meta is 300s longer, so positions can be ~300s off.
	// The real break sits at 380; naive 90s tolerance would penalise it
	// (|380-600|=220 > 90) below... nothing else exists, but the supported flag
	// records whether the pick looked plausible.
	p := startParams()
	a := &Analyzer{Params: p}
	rep := &Report{DurationSec: 1600, MetadataCount: len(meta)}
	rungs := []ladderRung{{gateOffsetDB: 0, gaps: []Gap{mkGap(380, 2.2, 20)}}}
	out := a.analyzeHardTarget(rep, rungs, nil, meta)
	if !out.CountMatched || out.AudioCount != 2 {
		t.Fatalf("expected 2 chapters, got %d (matched=%v)", out.AudioCount, out.CountMatched)
	}
	if out.Confidence < 0.9 {
		t.Fatalf("widened tolerance should make this a clean supported pick, conf=%.2f", out.Confidence)
	}
}

func TestExpectedInternalStartsExcludesFirst(t *testing.T) {
	meta := []catalog.AudioChapter{
		{Index: 1, StartSeconds: 0},
		{Index: 2, StartSeconds: 500.4},
		{Index: 3, StartSeconds: 750},
	}
	exp := expectedInternalStarts(meta)
	if len(exp) != 2 || math.Abs(exp[0]-500.4) > 1e-9 || math.Abs(exp[1]-750) > 1e-9 {
		t.Fatalf("expectedInternalStarts=%v, want [500.4 750]", exp)
	}
}

func TestLoosenGateClampsToSpeech(t *testing.T) {
	th := thresholds{SilenceDB: -45, SpeechDB: -20}
	// +30 dB would push the ceiling to -15, above (speech - 6 = -26). Clamp holds it.
	got := loosenGate(th, 30).SilenceDB
	if got != -26 {
		t.Fatalf("loosened gate=%.1f, want clamped to -26", got)
	}
	if loosenGate(th, 4).SilenceDB != -41 {
		t.Fatalf("modest loosen should be floor+offset")
	}
}

// Confidence collapses when a large share of the picks were desperation choices
// (short, off-position gaps admitted only at a heavily loosened gate), sending
// the book to review instead of writing junk boundaries.
func TestHardConfidencePenalisesForcedPicks(t *testing.T) {
	rep := &Report{CountMatched: true}
	res := convergeResult{
		need:         4,
		gateOffsetDB: 12,
		chosen: []chapterCandidate{
			{supported: true}, {supported: false}, {supported: false}, {supported: false},
		},
	}
	if conf := hardConfidence(rep, res); conf >= 0.6 {
		t.Fatalf("mostly-forced picks at +12 dB should fail the apply threshold, got %.2f", conf)
	}
	resClean := convergeResult{
		need:         4,
		gateOffsetDB: 0,
		chosen: []chapterCandidate{
			{supported: true}, {supported: true}, {supported: true}, {supported: true},
		},
	}
	if conf := hardConfidence(rep, resClean); conf < 0.9 {
		t.Fatalf("clean convergence should read high, got %.2f", conf)
	}
}
