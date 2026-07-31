package channels

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

func quietStreamer(t *testing.T) *channelStreamer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	streamer := newChannelStreamer(
		Channel{ID: "chan-test", Name: "Test", Codec: "mp3"},
		Dependencies{},
		NewScheduler(Dependencies{}),
		StreamerOptions{
			// No ffmpeg path: playItem returns immediately with an error, so
			// loop() spins through its scheduler-error branch. That is all
			// these tests need — they are about lifecycle, not audio.
			Logger:      log.New(io.Discard, "", 0),
			BaseContext: ctx,
		},
		nil,
	)
	t.Cleanup(func() { streamer.stopAndWait(context.Background()) })
	return streamer
}

// A listener arriving exactly as the previous one leaves must end up attached
// to a *running* streamer. The old code decided "should I start?" and "should I
// stop?" in two separate critical sections, so this interleaving left the new
// listener wired to a cancelled loop: no bytes, no error, silence until the
// client gave up.
func TestAttachDuringDetachLeavesStreamerRunning(t *testing.T) {
	streamer := quietStreamer(t)

	for i := 0; i < 200; i++ {
		_, detachFirst := streamer.Attach()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			detachFirst()
		}()
		var detachSecond func()
		go func() {
			defer wg.Done()
			_, detachSecond = streamer.Attach()
		}()
		wg.Wait()

		streamer.mu.Lock()
		listeners := len(streamer.listeners)
		running := streamer.done != nil
		streamer.mu.Unlock()

		if listeners > 0 && !running {
			t.Fatalf("iteration %d: %d listener(s) attached to a stopped streamer", i, listeners)
		}
		if detachSecond != nil {
			detachSecond()
		}
	}
}

// Once the last listener leaves, the loop must actually be signalled to stop —
// otherwise an idle channel keeps an ffmpeg alive indefinitely.
func TestLastDetachStopsTheLoop(t *testing.T) {
	streamer := quietStreamer(t)

	_, detach := streamer.Attach()
	streamer.mu.Lock()
	running := streamer.done != nil
	streamer.mu.Unlock()
	if !running {
		t.Fatal("attach did not start the loop")
	}

	detach()
	streamer.mu.Lock()
	running = streamer.done != nil
	streamer.mu.Unlock()
	if running {
		t.Fatal("last detach did not stop the loop")
	}
}

// detach lands in an HTTP handler's defer and can run twice if that handler
// panics after attaching. A double call must not corrupt the listener set.
func TestDetachIsIdempotent(t *testing.T) {
	streamer := quietStreamer(t)

	_, detachA := streamer.Attach()
	_, detachB := streamer.Attach()

	detachA()
	detachA()

	streamer.mu.Lock()
	listeners := len(streamer.listeners)
	streamer.mu.Unlock()
	if listeners != 1 {
		t.Fatalf("expected the second listener to survive a double detach, got %d listener(s)", listeners)
	}
	detachB()
}

// Cancelling the base context is what kills the ffmpeg subprocesses at
// shutdown. If a streamer ignored it, the transcoder would outlive the server.
func TestBaseContextCancellationStopsTheLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	streamer := newChannelStreamer(
		Channel{ID: "chan-cancel", Name: "Test", Codec: "mp3"},
		Dependencies{},
		NewScheduler(Dependencies{}),
		StreamerOptions{Logger: log.New(io.Discard, "", 0), BaseContext: ctx},
		nil,
	)

	streamer.Attach()
	streamer.mu.Lock()
	done := streamer.done
	streamer.mu.Unlock()
	if done == nil {
		t.Fatal("attach did not start the loop")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not exit after the base context was cancelled")
	}
}

func TestIsNetworkSource(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"/srv/media/show.mp3", false},
		{"file:///srv/media/show.mp3", false},
		{"http://stream.example/live", true},
		{"https://stream.example/live.m3u8", true},
	} {
		if got := isNetworkSource(tc.url); got != tc.want {
			t.Errorf("isNetworkSource(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}
