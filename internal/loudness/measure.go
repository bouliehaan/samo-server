package loudness

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrTooShort is returned for input with too little audio to measure. R128
// integrates over 400ms blocks with a relative gate, so a two-second sting
// produces a number that is technically valid and practically meaningless.
var ErrTooShort = errors.New("input too short to measure loudness")

// minMeasurableSeconds is the floor below which measurement is refused.
const minMeasurableSeconds = 3

// meterFilter is the measurement chain. Named once so the startup self-test
// and the real passes are provably the same thing — a self-test that verified
// a different filter string than the one used in anger would be worse than no
// self-test at all.
const meterFilter = "ebur128=peak=true"

// maxAnalysisSeconds is the most audio a single pass will decode.
//
// Integrated loudness is a gated average, and averages converge: ten minutes
// of programme pins a level to within a fraction of a decibel of what the full
// item would give. Decoding more than that buys nothing and costs everything —
// this library opens with 23-hour audiobooks, and reading them end to end meant
// the sweep spent hours on three files and never reached the music, which is
// where the level chaos actually lives.
const maxAnalysisSeconds = 600

// analysisWindow decides how much of an item to read, and from where.
//
// Anything at or under the cap is measured whole. Longer items are sampled
// from 5% in, which skips the theme, the label ident and the "this recording
// is presented by" that open most long-form audio and are mastered nothing
// like the body that follows.
func analysisWindow(durationSeconds int) (start, length int, windowed bool) {
	if durationSeconds <= maxAnalysisSeconds {
		return 0, 0, false
	}
	start = durationSeconds / 20
	if start > maxAnalysisSeconds {
		start = maxAnalysisSeconds
	}
	return start, maxAnalysisSeconds, true
}

// MeasureOptions describe one analysis pass.
type MeasureOptions struct {
	// Input is anything ffmpeg can open, and should be byte-for-byte the same
	// thing playback will open. Measuring a different rendition of the same
	// item than the one that airs is how a library ends up normalised to the
	// wrong numbers.
	Input string

	// Headers ride on HTTP inputs.
	Headers map[string]string

	// MaxSeconds bounds the pass. Zero measures to the end of the input.
	MaxSeconds int

	// StartSeconds skips into the input before measuring, so a window can be
	// taken from the body of a long item rather than its opening. Themes,
	// intros and label idents are not representative of what follows.
	StartSeconds int

	// Partial marks the result as a sample of something that may not be
	// uniform — a live stream. Set explicitly rather than inferred from
	// MaxSeconds, because a generous window taken from a long FILE is not the
	// same kind of guess: an audiobook is one narrator at one level for twenty
	// hours, so ten minutes of it characterises the whole thing, while forty
	// five seconds of a radio station characterises forty five seconds.
	Partial bool

	// DurationSeconds is the item's known length, used only to reject input
	// too short to measure. Zero means unknown, and unknown is allowed
	// through — ffmpeg's own error is a better report than a guess.
	DurationSeconds int

	// FFmpegPath is the absolute path to ffmpeg. Required.
	FFmpegPath string

	// Timeout bounds the whole pass. Analysis reads as fast as the disk and
	// CPU allow, so a finite file finishes far inside any sane budget; the
	// timeout is really there for a network source that stalls half way.
	Timeout time.Duration
}

// Measure runs an offline analysis pass and returns what it found.
//
// The pass decodes the input at maximum speed and throws the audio away —
// nothing is written, nothing is heard. On a local file this runs many times
// faster than real time, which is what makes measure-once-then-cache a
// practical strategy rather than a theoretical one.
//
// -threads 1 is deliberate. This runs on a box whose actual job is serving
// audio, often while transcoding a live channel, and an analysis pass that
// grabs every core to finish a second sooner is a worse neighbour than one
// that takes its time.
func Measure(ctx context.Context, opts MeasureOptions) (Measurement, error) {
	if strings.TrimSpace(opts.FFmpegPath) == "" {
		return Measurement{}, errors.New("loudness: ffmpeg path not configured")
	}
	if strings.TrimSpace(opts.Input) == "" {
		return Measurement{}, errors.New("loudness: no input")
	}
	if opts.DurationSeconds > 0 && opts.DurationSeconds < minMeasurableSeconds {
		return Measurement{}, ErrTooShort
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, opts.FFmpegPath, measureArgs(opts)...)
	cmd.Stdin = nil
	stderr := &tailBuffer{limit: 16 << 10}
	cmd.Stderr = stderr
	cmd.Stdout = nil

	// A non-zero exit disqualifies the pass, whatever it managed to print.
	//
	// This used to be lenient — parse first, and only judge the exit code if
	// there was nothing to parse — on the theory that a source dying in its
	// final moments could still have produced a usable measurement. That
	// theory cost ~19,000 wrong measurements: a filter option the container's
	// ffmpeg did not support made ebur128 print a summary full of zeroes and
	// then exit 1, and the leniency waved it straight through. A measurement
	// is only as trustworthy as the run that produced it.
	if runErr := cmd.Run(); runErr != nil {
		return Measurement{}, fmt.Errorf("loudness: ffmpeg failed: %w: %s", runErr, lastLine(stderr.String()))
	}
	measurement, parseErr := parseEBUR128Summary(stderr.String())
	if parseErr != nil {
		return Measurement{}, parseErr
	}
	measurement.Partial = opts.Partial
	measurement.MeasuredAt = time.Now().UTC()
	if !measurement.Valid() {
		return Measurement{}, fmt.Errorf(
			"loudness: unusable measurement (%.1f LUFS, peak %.1f dBTP)",
			measurement.IntegratedLUFS, measurement.TruePeakDBTP)
	}
	return measurement, nil
}

func measureArgs(opts MeasureOptions) []string {
	args := []string{"-nostdin", "-hide_banner"}
	if isHTTP(opts.Input) {
		if len(opts.Headers) > 0 {
			var b strings.Builder
			for key, value := range opts.Headers {
				b.WriteString(key)
				b.WriteString(": ")
				b.WriteString(value)
				b.WriteString("\r\n")
			}
			args = append(args, "-headers", b.String())
		}
		args = append(args,
			"-user_agent", "samo-server/loudness",
			"-rw_timeout", "30000000",
			"-analyzeduration", "3000000",
			"-probesize", "1000000",
		)
	}
	// Input-side seek: ffmpeg jumps in the container instead of decoding and
	// discarding everything up to the mark, which for a twenty-hour audiobook
	// is the difference between instant and useless.
	if opts.StartSeconds > 0 {
		args = append(args, "-ss", strconv.Itoa(opts.StartSeconds))
	}
	args = append(args, "-i", opts.Input)
	if opts.MaxSeconds > 0 {
		// Output-side duration: an input-side -t on a live stream throttles to
		// the wall clock, and the point of this pass is to run flat out.
		args = append(args, "-t", strconv.Itoa(opts.MaxSeconds))
	}
	args = append(args,
		"-threads", "1",
		// Cover art is a video stream; without -vn the null muxer is handed a
		// picture it has no idea what to do with.
		"-vn",
		// ebur128 is a meter and nothing else: it reports, it does not touch
		// the audio. loudnorm would also report — its analysis pass prints the
		// same figures as JSON, which is easier to parse — but it upsamples to
		// 192kHz for true peak and runs at about 7x realtime against this
		// filter's 46x. Across a 20,000-file library that is the difference
		// between a week of background CPU and a day, which is far more than a
		// tidier parser is worth.
		//
		// No framelog=quiet here, however tempting. It suppresses the per-frame
		// line ebur128 emits every 100ms and measured ~5% faster locally — but
		// the value is not accepted by ffmpeg 5.1, which is what Debian 12 ships
		// and therefore what the container runs. There it fails the filter, and
		// the failure mode is not a clean error: ebur128 still prints a summary,
		// just a degenerate one. The per-frame lines are harmless; the tail
		// buffer keeps only the end of stderr, where the summary is.
		"-af", meterFilter,
		"-f", "null",
		"-",
	)
	return args
}

// parseEBUR128Summary pulls the measurement out of ffmpeg's stderr.
//
// The summary block ebur128 prints at the end of a pass looks like:
//
//	Integrated loudness:
//	  I:         -22.0 LUFS
//	  Threshold: -32.0 LUFS
//
//	Loudness range:
//	  LRA:         0.0 LU
//	  Threshold: -42.0 LUFS
//	  LRA low:   -22.0 LUFS
//	  LRA high:  -22.0 LUFS
//
//	True peak:
//	  Peak:      -21.3 dBFS
//
// Two things make this fiddlier than it looks and both are handled below.
// "Threshold:" appears under two different headings and means something
// different each time, and "Peak:" is only a true peak because it sits under
// the "True peak:" heading — so the parse tracks which section it is in rather
// than matching labels globally.
func parseEBUR128Summary(stderr string) (Measurement, error) {
	start := strings.LastIndex(stderr, "Summary:")
	if start < 0 {
		return Measurement{}, errors.New("loudness: ffmpeg printed no measurement")
	}

	measurement := Measurement{
		IntegratedLUFS: math.NaN(),
		TruePeakDBTP:   math.NaN(),
	}
	haveIntegrated := false
	inTruePeak := false

	for _, raw := range strings.Split(stderr[start:], "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "True peak:"):
			inTruePeak = true
		case strings.HasPrefix(line, "Integrated loudness:"), strings.HasPrefix(line, "Loudness range:"):
			inTruePeak = false
		case strings.HasPrefix(line, "I:"):
			measurement.IntegratedLUFS = parseFloat(summaryValue(line))
			haveIntegrated = true
		// "LRA:" and not "LRA low:"/"LRA high:", which are a different figure.
		case strings.HasPrefix(line, "LRA:"):
			measurement.LoudnessRange = parseFloat(summaryValue(line))
		case inTruePeak && strings.HasPrefix(line, "Peak:"):
			measurement.TruePeakDBTP = parseFloat(summaryValue(line))
		}
	}
	if !haveIntegrated {
		return Measurement{}, errors.New("loudness: measurement has no integrated loudness")
	}
	return measurement, nil
}

// summaryValue takes "I:         -22.0 LUFS" and returns "-22.0".
func summaryValue(line string) string {
	colon := strings.Index(line, ":")
	if colon < 0 {
		return ""
	}
	fields := strings.Fields(line[colon+1:])
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// parseFloat turns one of ebur128's summary numbers into a float,
// mapping anything unreadable to NaN so Valid/peakKnown reject it rather than
// treating a parse failure as a real zero.
func parseFloat(raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return math.NaN()
	}
	return value
}

func isHTTP(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.LastIndex(s, "\n"); idx >= 0 {
		return strings.TrimSpace(s[idx+1:])
	}
	return s
}

// tailBuffer keeps the last N bytes of a subprocess's stderr. ffmpeg is chatty
// and the useful part — the report, or the error that explains its absence —
// is always at the end.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
