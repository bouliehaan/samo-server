package chapteraudio

import (
	"math"
	"math/rand"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// synthBook builds a deterministic speech-like signal with a KNOWN chapter
// structure so we can assert the detector recovers it. Each "chapter" is several
// tone bursts separated by short in-chapter pauses; chapters are separated by a
// longer pause. The detector must learn (per-file, from the distribution) that
// the long pauses are chapter breaks and the short ones are not — exactly the
// "don't just threshold at 2 seconds" requirement.
func synthBook(chapters int, longPause float64) (signal []float32, wantBoundaries []float64) {
	const (
		toneSeconds  = 4.0
		shortPause   = 0.4
		toneFreq     = 300.0  // inside the speech band
		toneAmp      = 0.3    // ~ -13.5 dBFS RMS
		noiseAmp     = 0.0005 // ~ -66 dBFS room tone
		burstsPerChp = 3
	)
	rng := rand.New(rand.NewSource(1))
	pos := 0.0

	appendTone := func(seconds float64) {
		n := int(seconds * SampleRate)
		for i := 0; i < n; i++ {
			t := float64(i) / SampleRate
			signal = append(signal, float32(toneAmp*math.Sin(2*math.Pi*toneFreq*t)))
		}
		pos += float64(n) / SampleRate
	}
	appendSilence := func(seconds float64) {
		n := int(seconds * SampleRate)
		for i := 0; i < n; i++ {
			signal = append(signal, float32(noiseAmp*(rng.Float64()*2-1)))
		}
		pos += float64(n) / SampleRate
	}

	for c := 0; c < chapters; c++ {
		for b := 0; b < burstsPerChp; b++ {
			appendTone(toneSeconds)
			if b < burstsPerChp-1 {
				appendSilence(shortPause)
			}
		}
		if c < chapters-1 {
			mid := pos + longPause/2
			wantBoundaries = append(wantBoundaries, mid)
			appendSilence(longPause)
		}
	}
	return signal, wantBoundaries
}

func TestDetectsChapterBreaksNotShortPauses(t *testing.T) {
	signal, want := synthBook(4, 2.5)

	feats := computeFeatures(signal)
	th := estimateThresholds(feats)

	// The adaptive gate must land between the room tone and the narration, and
	// must NOT have fallen back to a fixed constant region.
	if !(th.FloorDB < th.SilenceDB && th.SilenceDB < th.SpeechDB) {
		t.Fatalf("threshold ordering wrong: floor=%.1f silence=%.1f speech=%.1f", th.FloorDB, th.SilenceDB, th.SpeechDB)
	}
	if th.Separation < 0.5 {
		t.Fatalf("expected clean bimodal separation, got %.2f", th.Separation)
	}

	gaps := findGaps(feats, th, 0.25)
	if len(gaps) < 8 {
		t.Fatalf("expected to find both short and long pauses, got %d gaps", len(gaps))
	}

	cluster := clusterChapterGaps(gaps, 1.6, 0, 0)
	if got := len(cluster.ChapterGaps); got != len(want) {
		t.Fatalf("chapter-break count: got %d, want %d (split=%.2fs)", got, len(want), cluster.SplitSeconds)
	}
	// Split must sit cleanly between the short (0.4s) and long (2.5s) pauses.
	if cluster.SplitSeconds < 0.6 || cluster.SplitSeconds > 2.4 {
		t.Fatalf("split %.2fs did not separate 0.4s from 2.5s pauses", cluster.SplitSeconds)
	}
	for i, g := range cluster.ChapterGaps {
		if math.Abs(g.MidSec()-want[i]) > 0.5 {
			t.Errorf("boundary %d at %.2fs, want ~%.2fs", i, g.MidSec(), want[i])
		}
	}
	if cluster.Confidence < 0.6 {
		t.Errorf("expected high confidence on a clean synthetic book, got %.2f", cluster.Confidence)
	}
}

func TestVaryingPauseLengthStillSplits(t *testing.T) {
	// A narrator with a tighter between-chapter pause (1.2s) vs 0.4s in-chapter.
	signal, want := synthBook(5, 1.2)
	feats := computeFeatures(signal)
	th := estimateThresholds(feats)
	gaps := findGaps(feats, th, 0.25)
	cluster := clusterChapterGaps(gaps, 1.6, 0, 0)
	if got := len(cluster.ChapterGaps); got != len(want) {
		t.Fatalf("count: got %d want %d (split=%.2f)", got, len(want), cluster.SplitSeconds)
	}
}

func TestAlignNamesMapsByTimeAndDropsSurplus(t *testing.T) {
	// Audio found 4 chapters; metadata has 3 titles whose times DRIFT (the whole
	// reason we're doing this). Names must still land on the right boundaries in
	// order, and the 4th audio chapter goes unnamed.
	audioStarts := []float64{0, 100, 200, 300}
	meta := []catalog.AudioChapter{
		{Index: 1, Title: "Prologue", StartSeconds: 3},   // drifted +3
		{Index: 2, Title: "The Road", StartSeconds: 95},  // drifted -5
		{Index: 3, Title: "The City", StartSeconds: 208}, // drifted +8
	}
	matches := alignNames(audioStarts, meta, 60)
	wantMeta := []int{0, 1, 2, -1}
	for i, w := range wantMeta {
		if matches[i].MetaIndex != w {
			t.Errorf("chapter %d matched meta %d, want %d", i, matches[i].MetaIndex, w)
		}
	}
	title, named := titleFor(3, matches[3], meta)
	if named || title != "Chapter 4" {
		t.Errorf("surplus chapter: got %q named=%v, want \"Chapter 4\" named=false", title, named)
	}
}

func TestMergeBoundariesPrefersFileSeamAndTrimsEdges(t *testing.T) {
	cands := []boundaryCand{
		{Time: 0.5, FromFile: false},  // too close to start -> trimmed
		{Time: 100, FromFile: false},  // gap
		{Time: 100.8, FromFile: true}, // file seam ~ same place -> wins exact time
		{Time: 200, FromFile: false},
		{Time: 299.6, FromFile: false}, // too close to end (total 300) -> trimmed
	}
	merged := mergeBoundaries(cands, 1.5, 2.0, 300)
	if len(merged) != 2 {
		t.Fatalf("merged to %d boundaries, want 2: %+v", len(merged), merged)
	}
	if !merged[0].FromFile || math.Abs(merged[0].Time-100.8) > 1e-9 {
		t.Errorf("first boundary should be the file seam at 100.8, got %+v", merged[0])
	}
}
