// Package chapteraudio derives audiobook chapter boundaries from the audio
// itself instead of trusting embedded or Audnexus markers, which are timed
// against a DIFFERENT master edition and drift further the deeper you get into a
// book. A narrator pauses between chapters; those pauses are real positions in
// THIS file and cannot drift. We decode the audio, find the significant silences
// the way an engineer would read a spectrogram (band-limited to the speech range
// so rumble and hiss can't fool us, with an adaptive noise floor derived from
// the file's own energy distribution — never a fixed "2 second" rule), cluster
// the long gaps into chapter breaks, and then borrow only the NAMES from
// embedded/Audnexus metadata, mapped onto the boundaries the audio proves.
package chapteraudio

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strings"
)

// SampleRate is the rate we decode audiobook audio to for analysis. 16 kHz
// preserves the full speech band (Nyquist 8 kHz) while keeping the decoded
// stream tiny (~64 KB/s), so even a 12-hour book streams through without ever
// buffering the whole waveform in memory.
const SampleRate = 16000

// Speech-band bounds for the pre-analysis bandpass. We ask ffmpeg to band-limit
// to roughly the speech range BEFORE we measure energy. This is the cheap "look
// at the spectrum, not just the volume" step that makes the detector smart
// rather than stupid:
//
//   - The high-pass strips sub-bass rumble — HVAC, truck-by, mic handling,
//     plosive thump — that can sit at -40 dBFS during an otherwise dead-silent
//     gap and trick a naive amplitude gate into thinking the narrator is still
//     talking.
//   - The low-pass strips tape hiss and sibilant fizz that linger after a line
//     ends, so the tail of a real pause reads as the silence it is.
//
// What's left is the band where narration actually lives, so "quiet here" means
// "quiet where it counts."
const (
	highpassHz = 85
	lowpassHz  = 7500
)

// errStopDecode lets a consumer halt streamPCM early without it being treated as
// a decode failure (e.g. a future "first N minutes only" probe).
var errStopDecode = errors.New("chapteraudio: stop decode")

// streamPCM decodes one audio file to mono 16 kHz float32 PCM through ffmpeg,
// applying the speech-band bandpass, and feeds the samples to onChunk in blocks.
// The slice handed to onChunk is REUSED between calls — copy anything you need to
// keep. ffmpeg does the decode + filtering in C, so this stays cheap; Go only
// ever sees a float stream it folds into a loudness envelope.
func streamPCM(ctx context.Context, ffmpegPath, path string, onChunk func([]float32) error) error {
	if strings.TrimSpace(ffmpegPath) == "" {
		ffmpegPath = "ffmpeg"
	}
	bandpass := fmt.Sprintf("highpass=f=%d,lowpass=f=%d", highpassHz, lowpassHz)
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-nostdin", "-hide_banner", "-v", "error",
		"-i", path,
		"-map", "0:a:0", // first audio stream; errors if the file has no audio
		"-af", bandpass,
		"-ac", "1",
		"-ar", fmt.Sprint(SampleRate),
		"-f", "f32le",
		"-",
	)
	cmd.Stdin = nil

	// Bound stderr so a chatty ffmpeg can't grow memory without limit; the first
	// line is all we surface anyway.
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 8 << 10}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("chapteraudio: ffmpeg stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("chapteraudio: start ffmpeg: %w", err)
	}

	readErr := decodeF32LE(bufio.NewReaderSize(stdout, 1<<16), onChunk)
	// Always reap the child so we don't leak a zombie, even on a read error.
	waitErr := cmd.Wait()

	if readErr != nil && !errors.Is(readErr, errStopDecode) {
		return readErr
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("chapteraudio: ffmpeg %q: %s", path, firstLine(msg))
		}
		return fmt.Errorf("chapteraudio: ffmpeg %q: %w", path, waitErr)
	}
	return nil
}

// decodeF32LE reads little-endian float32 samples from r and dispatches them to
// onChunk, carrying any partial trailing sample bytes across reads so a sample
// split across two Read calls is reconstructed correctly.
func decodeF32LE(r io.Reader, onChunk func([]float32) error) error {
	const chunkSamples = 1 << 14 // 16384 samples (~1 s at 16 kHz) per callback
	raw := make([]byte, chunkSamples*4)
	out := make([]float32, chunkSamples)
	var carry [4]byte
	carryN := 0

	for {
		copy(raw, carry[:carryN]) // re-seat leftover bytes at the front
		n, err := r.Read(raw[carryN:])
		total := carryN + n
		usable := total - (total % 4)

		samples := out[:usable/4]
		for i := 0; i < usable; i += 4 {
			bits := uint32(raw[i]) | uint32(raw[i+1])<<8 | uint32(raw[i+2])<<16 | uint32(raw[i+3])<<24
			samples[i/4] = math.Float32frombits(bits)
		}
		carryN = total - usable
		copy(carry[:], raw[usable:total])

		if len(samples) > 0 {
			if cbErr := onChunk(samples); cbErr != nil {
				return cbErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// limitedWriter drops everything past the first n bytes.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	if len(p) > l.n {
		_, _ = l.w.Write(p[:l.n])
		l.n = 0
		return len(p), nil
	}
	l.n -= len(p)
	return l.w.Write(p)
}
