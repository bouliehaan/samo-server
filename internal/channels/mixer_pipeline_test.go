package channels

import (
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The whole stage against a real ffmpeg: a tone decoded, mixed, encoded to
// MP3, and the MP3 decoded again to look at the level envelope. What is
// asserted is what a listener would hear — a fade-in at the top, a fade-out
// into the play window, the next item under a crossfade — rather than what
// any one process was told to do.
func TestThePipelineFadesWhatAListenerHears(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on this machine")
	}
	dir := t.TempDir()
	tone := func(name string, hz int) string {
		path := filepath.Join(dir, name)
		out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin",
			"-f", "lavfi", "-i", "sine=frequency="+itoa(hz)+":duration=20",
			"-af", "volume=0.5", "-ac", "2", "-ar", "44100", path).CombinedOutput()
		if err != nil {
			t.Fatalf("could not make a tone: %v\n%s", err, out)
		}
		return path
	}
	low, high := tone("low.wav", 220), tone("high.wav", 880)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamer := newChannelStreamer(
		Channel{ID: "chan-pipe", Name: "Pipe", Codec: "mp3", BitrateKbps: 128},
		Dependencies{}, NewScheduler(Dependencies{}),
		StreamerOptions{FFmpegPath: ffmpeg, Logger: log.New(io.Discard, "", 0), BaseContext: ctx},
		nil,
	)
	t.Cleanup(func() { streamer.stopAndWait(context.Background()) })

	// Everything the encoder produces, as a listener would receive it.
	ear := &listener{ch: make(chan []byte, 4096), jitter: 4096}
	streamer.mu.Lock()
	streamer.listeners[ear] = struct{}{}
	streamer.mu.Unlock()
	captured := filepath.Join(dir, "captured.mp3")
	file, err := os.Create(captured)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for chunk := range ear.ch {
			_, _ = file.Write(chunk)
		}
	}()

	// One: two seconds of the low tone, faded out over its last second.
	written, err := streamer.playItemWithFade(ctx, PlaybackItem{
		URL: low, Title: "low", MaxDuration: 2 * time.Second, FadeOut: time.Second,
	}, 500*time.Millisecond)
	if written == 0 || err != context.DeadlineExceeded {
		t.Fatalf("first item: written %d, err %v", written, err)
	}
	// Two: the high tone, cut after two seconds under the skip fade.
	itemCtx, itemCancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() {
		_, err := streamer.playItemWithFade(itemCtx, PlaybackItem{URL: high, Title: "high"}, fadeInAfterCut)
		result <- err
	}()
	time.Sleep(2 * time.Second)
	streamer.cutFade.Store(int64(fadeOutSkip))
	itemCancel()
	if err := <-result; err != context.Canceled {
		t.Fatalf("second item ended with %v, want cancelled", err)
	}
	// Let the skip fade and a little silence go out, then stop the stage.
	time.Sleep(800 * time.Millisecond)
	streamer.stopMixer()
	streamer.mu.Lock()
	delete(streamer.listeners, ear)
	streamer.mu.Unlock()
	ear.close()
	<-done
	_ = file.Close()

	// Decode what was captured back to PCM and measure it in 100ms windows.
	pcm, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", captured, "-ac", "1", "-ar", "8000", "-f", "s16le", "pipe:1").Output()
	if err != nil {
		t.Fatalf("the captured stream did not decode as MP3: %v", err)
	}
	window := 8000 / 10 * 2
	levels := []float64{}
	for start := 0; start+window <= len(pcm); start += window {
		levels = append(levels, peakOf(pcm[start:start+window]))
	}
	t.Logf("captured %.1fs, level envelope (100ms): %v", float64(len(pcm))/16000, compact(levels))
	if len(levels) < 40 {
		t.Fatalf("only %.1fs of audio came out of the stage", float64(len(pcm))/16000)
	}

	full := 0.0
	for _, level := range levels[8:16] {
		if level > full {
			full = level
		}
	}
	if full < 500 {
		t.Fatalf("the first item never reached level: %v", levels[:20])
	}
	// A fade-in: the first window is well under the level the item reaches.
	if levels[0] > full*0.6 {
		t.Fatalf("the first item started at %.0f of %.0f — no fade-in", levels[0], full)
	}
	// A fade-out into the play window: the last windows of the first item
	// fall away, and it is quiet where the item ends (2.0s) before the second
	// item has come up.
	if levels[18] > levels[12] || levels[19] > full*0.5 {
		t.Fatalf("no fade-out into the play window: %v", levels[10:24])
	}
	// The second item reaches level, and ends under a short fade rather than
	// running on or cutting dead.
	second := 0.0
	for _, level := range levels[30:40] {
		if level > second {
			second = level
		}
	}
	if second < full*0.8 {
		t.Fatalf("the second item never reached level: %v", levels[20:44])
	}
	tail := levels[len(levels)-3:]
	if tail[2] > full*0.2 {
		t.Fatalf("the stream did not go quiet after the cut: %v", tail)
	}
}

func itoa(n int) string {
	return string(rune('0'+n/100)) + string(rune('0'+n/10%10)) + string(rune('0'+n%10))
}

func compact(levels []float64) []int {
	out := make([]int, len(levels))
	for i, level := range levels {
		out[i] = int(level / 100)
	}
	return out
}
