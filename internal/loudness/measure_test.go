package loudness

import (
	"errors"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A real analysis-pass stderr: ffmpeg's ordinary chatter, then the summary.
const sampleStderr = `Input #0, mp3, from '/music/track.mp3':
  Duration: 00:03:41.22, start: 0.025057, bitrate: 320 kb/s
Stream mapping:
  Stream #0:0 -> #0:0 (mp3 (mp3float) -> pcm_s16le (native))
[Parsed_ebur128_0 @ 0xc01004fc0] Summary:

  Integrated loudness:
    I:          -9.42 LUFS
    Threshold: -19.63 LUFS

  Loudness range:
    LRA:         4.80 LU
    Threshold: -29.70 LUFS
    LRA low:   -12.10 LUFS
    LRA high:   -7.30 LUFS

  True peak:
    Peak:       -0.18 dBFS
[out#0/null @ 0xc01004900] video:0KiB audio:41344KiB subtitle:0KiB
`

func TestParseEBUR128Summary(t *testing.T) {
	m, err := parseEBUR128Summary(sampleStderr)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if math.Abs(m.IntegratedLUFS-(-9.42)) > 0.001 {
		t.Errorf("integrated = %v, want -9.42", m.IntegratedLUFS)
	}
	if math.Abs(m.TruePeakDBTP-(-0.18)) > 0.001 {
		t.Errorf("true peak = %v, want -0.18", m.TruePeakDBTP)
	}
	if math.Abs(m.LoudnessRange-4.80) > 0.001 {
		t.Errorf("LRA = %v, want 4.80", m.LoudnessRange)
	}

	// ebur128 only ever reports; there is no processed-output variant of these
	// figures to pick up by mistake, which is part of why it is the right
	// filter for a package whose whole point is not to process anything.
	plan := DefaultTarget.Plan(m)
	if math.Abs(plan.GainDB-(-6.6)) > 0.05 || plan.Limit {
		t.Errorf("plan = %+v, want a clean -6.6 dB cut with no limiter", plan)
	}
}

// Only the LAST summary counts: a filtergraph rebuild mid-file can emit more
// than one, and the earlier ones describe audio that was thrown away.
func TestParseEBUR128SummaryTakesTheLast(t *testing.T) {
	noisy := `[Parsed_ebur128_0 @ 0x1] Summary:

  Integrated loudness:
    I:         -30.00 LUFS
` + sampleStderr
	m, err := parseEBUR128Summary(noisy)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if math.Abs(m.IntegratedLUFS-(-9.42)) > 0.001 {
		t.Errorf("integrated = %v, want -9.42", m.IntegratedLUFS)
	}
}

func TestParseEBUR128SummaryRejectsNoReport(t *testing.T) {
	if _, err := parseEBUR128Summary("/music/x.mp3: Invalid data found when processing input\n"); err == nil {
		t.Fatal("expected an error when ffmpeg printed no report")
	}
}

// Digital silence parses fine and must then be rejected by the policy, not
// boosted by the maximum.
func TestSilenceIsUnusable(t *testing.T) {
	silent := `Summary:

  Integrated loudness:
    I:           -inf LUFS

  True peak:
    Peak:        -inf dBFS
`
	m, err := parseEBUR128Summary(silent)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Valid() {
		t.Fatal("silence must not be treated as a usable measurement")
	}
	if plan := DefaultTarget.Plan(m); !plan.Zero() {
		t.Fatalf("plan = %+v, want no adjustment for silence", plan)
	}
}

func TestMeasureRejectsShortInput(t *testing.T) {
	_, err := Measure(t.Context(), MeasureOptions{
		Input:           "/music/sting.wav",
		FFmpegPath:      "/usr/bin/ffmpeg",
		DurationSeconds: 2,
	})
	if !errors.Is(err, ErrTooShort) {
		t.Fatalf("err = %v, want ErrTooShort", err)
	}
}

func TestMeasureArgs(t *testing.T) {
	t.Run("finite local file", func(t *testing.T) {
		args := strings.Join(measureArgs(MeasureOptions{Input: "/music/track.flac"}), " ")
		for _, want := range []string{
			"-i /music/track.flac",
			"-af ebur128=peak=true",
			"-f null",
			// Analysis must not grab every core on a box whose real job is
			// serving audio.
			"-threads 1",
		} {
			if !strings.Contains(args, want) {
				t.Errorf("args missing %q\ngot: %s", want, args)
			}
		}
		if strings.Contains(args, "-t ") {
			t.Errorf("a finite file must be measured whole, not windowed\ngot: %s", args)
		}
		// framelog is not accepted by ffmpeg 5.1 (Debian 12, what the container
		// runs). Adding it there breaks the filter and yields a summary full of
		// zeroes that parses cleanly — which is how ~19,000 files got cached at
		// 0.0 LUFS and cut by 16 dB on air. It bought about 5%.
		if strings.Contains(args, "framelog") {
			t.Errorf("framelog must not appear: unsupported on ffmpeg 5.1\ngot: %s", args)
		}
	})

	t.Run("live stream is windowed", func(t *testing.T) {
		args := strings.Join(measureArgs(MeasureOptions{
			Input:      "http://stream.example/live",
			MaxSeconds: 45,
		}), " ")
		if !strings.Contains(args, "-i http://stream.example/live -t 45") {
			t.Errorf("-t must follow -i so the limit applies to output, not input pacing\ngot: %s", args)
		}
		if !strings.Contains(args, "-rw_timeout") {
			t.Errorf("a network input needs an I/O timeout or a dead stream holds the slot\ngot: %s", args)
		}
	})
}

func TestRequestKeyIsStableAcrossCallers(t *testing.T) {
	// The channel scheduler hands over a path; the samo-radio resolver hands
	// over the same path from the catalog. Both must hit the same cache row,
	// or every file gets measured twice and levelled from two rows that can
	// drift apart.
	fromScheduler := RequestFor("/music/album/01.flac", 240, false).key()
	fromResolver := Request{Input: "/music/album/01.flac"}.key()
	if fromScheduler != fromResolver {
		t.Fatalf("%q != %q", fromScheduler, fromResolver)
	}
	if fromScheduler != "file:/music/album/01.flac" {
		t.Fatalf("key = %q, want the backfill sweep's 'file:' || path form", fromScheduler)
	}

	if key := RequestFor("https://cdn.example/ep.mp3", 0, false).key(); key != "url:https://cdn.example/ep.mp3" {
		t.Fatalf("remote key = %q", key)
	}
}

// End to end against a real ffmpeg, and the clearest statement of what this
// package is for: two files mastered sixteen decibels apart come out level.
//
// The trims are calibrated against a reference tone rather than assumed,
// because ffmpeg's sine generator does not produce a documented absolute
// level and hard-coding one produces a test that measures the generator
// instead of the code. Calibrating puts both files at realistic programme
// levels, which is where the policy's caps are not the thing under test.
func TestMeasureLevelsTwoRealFiles(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()

	tone := func(name string, trimDB float64) string {
		t.Helper()
		path := filepath.Join(dir, name)
		build := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=6:sample_rate=48000",
			"-af", fmt.Sprintf("volume=%.2fdB", trimDB), "-ac", "2", path)
		if out, err := build.CombinedOutput(); err != nil {
			t.Skipf("could not synthesise a test tone: %v: %s", err, out)
		}
		return path
	}
	measure := func(path string) Measurement {
		t.Helper()
		m, err := Measure(t.Context(), MeasureOptions{
			Input: path, FFmpegPath: ffmpeg, DurationSeconds: 6, Timeout: time.Minute,
		})
		if err != nil {
			t.Fatalf("measure %s: %v", path, err)
		}
		if !m.Valid() {
			t.Fatalf("measurement of %s is unusable: %+v", path, m)
		}
		return m
	}

	// What this ffmpeg's sine actually measures at, so the trims below mean
	// something.
	base := measure(tone("reference.wav", 0)).IntegratedLUFS

	// A loud modern master and a quiet archive recording, 16 LU apart.
	const loudLUFS, quietLUFS = -10.0, -26.0
	loudM := measure(tone("loud.wav", loudLUFS-base))
	quietM := measure(tone("quiet.wav", quietLUFS-base))

	if gap := loudM.IntegratedLUFS - quietM.IntegratedLUFS; math.Abs(gap-16) > 1.5 {
		t.Fatalf("measured gap is %.1f LU, want the 16 the files were built with "+
			"(loud %.1f, quiet %.1f LUFS)", gap, loudM.IntegratedLUFS, quietM.IntegratedLUFS)
	}

	loudPlan, quietPlan := DefaultTarget.Plan(loudM), DefaultTarget.Plan(quietM)
	loudOut := loudM.IntegratedLUFS + loudPlan.GainDB
	quietOut := quietM.IntegratedLUFS + quietPlan.GainDB

	if residual := math.Abs(loudOut - quietOut); residual > 1 {
		t.Errorf("after levelling they are still %.1f LU apart (%.1f vs %.1f LUFS)",
			residual, loudOut, quietOut)
	}
	for _, out := range []float64{loudOut, quietOut} {
		if math.Abs(out-DefaultTarget.LUFS) > 1 {
			t.Errorf("levelled to %.1f LUFS, want %.1f", out, DefaultTarget.LUFS)
		}
	}

	// A steady tone has almost no crest factor, so there is headroom to spare
	// and the limiter must stay out of circuit for both.
	if loudPlan.Limit || quietPlan.Limit {
		t.Error("a sine needs no peak limiting; the limiter should never engage here")
	}
}

// The sweep opens on 23-hour audiobooks. Reading those end to end is what
// stopped the first deploy from measuring anything at all, so the window has
// to bound the work and start past the intro.
func TestAnalysisWindow(t *testing.T) {
	cases := []struct {
		name               string
		duration           int
		wantStart, wantLen int
		wantWindowed       bool
	}{
		{"a song is measured whole", 240, 0, 0, false},
		{"a half-hour podcast is measured whole", 600, 0, 0, false},
		{"an hour-long show is sampled", 3600, 180, 600, true},
		{"a 23-hour audiobook is sampled, not read", 83227, 600, 600, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, length, windowed := analysisWindow(tc.duration)
			if windowed != tc.wantWindowed || start != tc.wantStart || length != tc.wantLen {
				t.Fatalf("got start=%d len=%d windowed=%v, want start=%d len=%d windowed=%v",
					start, length, windowed, tc.wantStart, tc.wantLen, tc.wantWindowed)
			}
			// However long the item, one pass never decodes more than the cap.
			if length > maxAnalysisSeconds {
				t.Fatalf("window of %ds exceeds the %ds cap", length, maxAnalysisSeconds)
			}
		})
	}
}

// -ss must precede -i to be a container seek. After -i it decodes and discards
// everything up to the mark, which on a 23-hour book is the entire problem.
func TestMeasureArgsSeeksBeforeInput(t *testing.T) {
	args := measureArgs(MeasureOptions{Input: "/books/long.m4b", StartSeconds: 600, MaxSeconds: 600})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-ss 600 -i /books/long.m4b -t 600") {
		t.Fatalf("want a container seek then a bounded read, got: %s", joined)
	}
}

// A windowed FILE is not partial; a windowed STREAM is. The difference decides
// whether the measurement expires weekly, and getting it wrong means
// re-scanning every audiobook in the library forever.
func TestWindowedFileIsNotMarkedPartial(t *testing.T) {
	svc := &Service{target: DefaultTarget.normalized()}
	book := RequestFor("/books/long.m4b", 83227, false)

	fresh := record{
		Measurement: Measurement{IntegratedLUFS: -20, TruePeakDBTP: -3},
		Fingerprint: book.fingerprint(),
		MeasuredAt:  time.Now().Add(-partialTTL - time.Hour),
	}
	if !svc.fresh(fresh, book) {
		t.Error("a file's measurement must not expire with age; the fingerprint covers change")
	}

	station := RequestFor("http://stream.example/live", 0, true)
	stale := record{
		Measurement: Measurement{IntegratedLUFS: -18, TruePeakDBTP: -2, Partial: true},
		Fingerprint: station.fingerprint(),
		MeasuredAt:  time.Now().Add(-partialTTL - time.Hour),
	}
	if svc.fresh(stale, station) {
		t.Error("a live station's sample must expire; its level drifts")
	}
}

// The exact stderr an ffmpeg too old for the filter option produced. Every
// heading is present and every field parses, so nothing upstream of the
// validity rules can tell it apart from a real measurement. This is the
// regression test for the worst bug in this package's history.
func TestDegenerateSummaryIsRejected(t *testing.T) {
	const degenerate = `[Parsed_ebur128_0 @ 0x1] Summary:

  Integrated loudness:
    I:           0.0 LUFS
    Threshold:    -inf LUFS

  Loudness range:
    LRA:         0.0 LU

  True peak:
    Peak:       -inf dBFS
Error reinitializing filters!
Failed to inject frame into filter network: Invalid argument
Conversion failed!
`
	m, err := parseEBUR128Summary(degenerate)
	if err != nil {
		t.Fatalf("it parses — that is the whole problem: %v", err)
	}
	if m.Valid() {
		t.Fatal("0.0 LUFS with a -inf peak must be rejected: loudness cannot exceed peak")
	}
	if plan := DefaultTarget.Plan(m); !plan.Zero() {
		t.Fatalf("plan = %+v, want no adjustment; this is what cut 19,000 files by 16 dB", plan)
	}
}

// A non-zero ffmpeg exit disqualifies the pass even when it printed a summary.
func TestMeasureRejectsFailedRun(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	_, err = Measure(t.Context(), MeasureOptions{
		Input:      filepath.Join(t.TempDir(), "does-not-exist.flac"),
		FFmpegPath: ffmpeg,
		Timeout:    30 * time.Second,
	})
	if err == nil {
		t.Fatal("a failed ffmpeg run must not yield a measurement")
	}
}
