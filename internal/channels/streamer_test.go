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
// The loop outlives the last listener by a short grace period, then stops.
//
// Tearing it down the instant the socket closes made every reconnect a fresh
// start: the programming decision is re-run and whatever was on air is
// abandoned. The daemon reconnects for entirely ordinary reasons — a skip, an
// output-device change, a blip on the loopback socket — so a four-hour episode
// could be dropped a minute in, having already been written to the play log as
// aired, which then rests it for hours.
func TestTheLoopLingersForAReconnectThenStops(t *testing.T) {
	streamer := quietStreamer(t)
	streamer.idleLinger = 50 * time.Millisecond

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
	if !running {
		t.Fatal("the loop stopped immediately, so a reconnect cannot rejoin it")
	}

	// Somebody comes straight back, as the daemon does after a skip.
	_, detachAgain := streamer.Attach()
	time.Sleep(120 * time.Millisecond)
	streamer.mu.Lock()
	running = streamer.done != nil
	pending := streamer.idleStop != nil
	streamer.mu.Unlock()
	if !running {
		t.Fatal("a reconnect inside the grace period did not rejoin the running loop")
	}
	if pending {
		t.Fatal("reattaching must cancel the pending teardown")
	}

	// And with nobody there, it does actually stop.
	detachAgain()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		streamer.mu.Lock()
		running = streamer.done != nil
		streamer.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the loop never stopped after the grace period expired")
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

// Retrying a dead source as fast as ffmpeg can exit is what dead air sounds
// like: thousands of attempts an hour, a play-log row for each, and no audio.
func TestFailureBackoffGrowsAndCaps(t *testing.T) {
	if got := failureBackoff(1); got != 2*time.Second {
		t.Fatalf("first retry: got %v", got)
	}
	if got := failureBackoff(2); got != 4*time.Second {
		t.Fatalf("second retry: got %v", got)
	}
	if got := failureBackoff(3); got != 8*time.Second {
		t.Fatalf("third retry: got %v", got)
	}
	for _, attempt := range []int{6, 20, 200} {
		if got := failureBackoff(attempt); got != 30*time.Second {
			t.Fatalf("attempt %d must cap at 30s, got %v", attempt, got)
		}
	}
	// Defensive: a zero or negative count must not produce a zero delay, which
	// would reinstate the tight loop this exists to prevent.
	for _, attempt := range []int{0, -1} {
		if got := failureBackoff(attempt); got < time.Second {
			t.Fatalf("attempt %d produced %v, which is still a spin", attempt, got)
		}
	}
}

// Connecting to a live stream is a slower problem than a gap in one that is
// already flowing, and holding both to the same budget kills healthy stations
// before they ever speak.
func TestStartupGetsMorePatienceThanAStall(t *testing.T) {
	if startupTimeout <= stallTimeout {
		t.Fatalf("first-byte budget (%v) must exceed the mid-stream stall budget (%v)", startupTimeout, stallTimeout)
	}
	// It also has to outlast ffmpeg's own network timeout, or the watchdog
	// cancels before ffmpeg has had its chance to report a real error — and an
	// error is far more useful than a silent cancellation.
	ffmpegTimeout := time.Duration(networkIOTimeoutMicros) * time.Microsecond
	if startupTimeout <= ffmpegTimeout {
		t.Fatalf("startup budget (%v) must outlast ffmpeg's own I/O timeout (%v)", startupTimeout, ffmpegTimeout)
	}
}
