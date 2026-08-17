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

// An item that will not play must not be handed back immediately.
//
// Discarding the play-log row keeps a dead item from counting as heard, and the
// backoff keeps the retry from spinning — but neither tells SELECTION anything,
// so the next decision picks the same unplayable episode again. On a cycle that
// alternates break and obligation that produces music, a dead pick nobody
// hears, music, a dead pick: the station sounds like it has given up on
// podcasts. Jacob's 09:00 was exactly this, with one bad Shawn Ryan episode.
func TestAnUnplayableItemIsPassedOver(t *testing.T) {
	skips := NewSkipRegistry(func() time.Time { return time.Now().UTC() })

	item := PlaybackItem{
		ItemRef: "episode:dead", Title: "Scott Payne", SourceID: "pod-ryan",
		SourceLabel: "Shawn Ryan Show", Category: "talk",
	}

	// What the streamer does when an item produces no audio.
	if item.ItemRef != "" {
		skips.SuppressRef(item.ItemRef)
	}
	if !skips.RefSuppressed(item.ItemRef) {
		t.Fatal("a dead item is still on offer, so the next decision picks it again")
	}
	// Its siblings are still fine — one bad episode is not a bad show.
	if skips.Suppressed("pod-ryan") {
		t.Fatal("one failure should not take the whole show off the air")
	}

	// But a run of them should.
	for failures := 1; failures <= deadSourceAfter; failures++ {
		if failures >= deadSourceAfter {
			skips.Suppress(item.SourceID, DefaultSkipSuppression)
		}
	}
	if !skips.Suppressed("pod-ryan") {
		t.Fatalf("after %d failures in a row the station should step off the source",
			deadSourceAfter)
	}
}

// A listener that stalls and then keeps up again must end up LIVE, not
// permanently late.
//
// Jacob's station: chrony-synced server, decisions recorded at exactly
// 08:00:00, and a booked show he heard thirty seconds after his watch said the
// hour turned. The scheduler was never the problem — the queue in front of him
// was. listenerBuffer holds ~40s at 192kbps, both ends of it run at exactly 1x,
// and nothing in the loop ever reads faster than real time, so every transient
// the connection suffers is added to the standing delay and never given back.
//
// Reverting catchUp fails this: the depth stays where the stall put it.
func TestASlowListenerCatchesUpInsteadOfStayingLate(t *testing.T) {
	jitter := jitterChunks(192)
	lis := &listener{ch: make(chan []byte, listenerBuffer), jitter: jitter}

	// A stall: the HTTP handler is blocked writing to a socket and reads
	// nothing, while the encoder keeps producing.
	for i := 0; i < listenerBuffer-1; i++ {
		if !lis.send([]byte{byte(i)}) {
			t.Fatalf("chunk %d was refused while there was still room", i)
		}
	}

	if depth := len(lis.ch); depth > jitter {
		t.Fatalf("listener sat %d chunks behind; a jitter buffer must not exceed %d",
			depth, jitter)
	}

	// And what survived is the NEWEST audio — catching up means skipping
	// forward to what the station is playing now, not replaying the backlog.
	newest := byte(listenerBuffer - 2)
	var last byte
	for len(lis.ch) > 0 {
		chunk := <-lis.ch
		last = chunk[0]
	}
	if last != newest {
		t.Fatalf("listener rejoined at chunk %d, want the most recent chunk %d", last, newest)
	}
}

// The catch-up must not drop a listener that is keeping up perfectly well.
func TestAHealthyListenerIsNeverDropped(t *testing.T) {
	lis := &listener{ch: make(chan []byte, listenerBuffer), jitter: jitterChunks(192)}
	for i := 0; i < listenerBuffer*4; i++ {
		if !lis.send([]byte{byte(i)}) {
			t.Fatalf("chunk %d was refused; a listener that never falls behind must not be dropped", i)
		}
		// The handler consumes each chunk as it arrives.
		<-lis.ch
	}
	if depth := len(lis.ch); depth != 0 {
		t.Fatalf("expected a drained queue, got %d chunk(s)", depth)
	}
}

// The jitter target is a duration, so the standing delay must be broadly the
// same at any bitrate. A fixed chunk count would make a 96kbps channel twice as
// late as a 192kbps one for no stated reason. The floor is allowed to win —
// see listenerJitterFloor — but nothing else is.
func TestJitterTargetIsTheSameDelayAtEveryBitrate(t *testing.T) {
	for _, bitrate := range []int{96, 128, 192, 320} {
		chunks := jitterChunks(bitrate)
		perSecond := float64(bitrate) * 1000 / 8
		queued := time.Duration(float64(chunks) * streamChunk / perSecond * float64(time.Second))
		allowed := listenerJitterTarget
		if floor := time.Duration(float64(listenerJitterFloor) * streamChunk / perSecond * float64(time.Second)); floor > allowed {
			allowed = floor
		}
		if queued > allowed {
			t.Errorf("at %dkbps the queue holds %s, over the %s it is allowed", bitrate, queued, allowed)
		}
		// And it must not collapse to nothing, or normal jitter trims constantly.
		if chunks < listenerJitterFloor {
			t.Errorf("at %dkbps the depth fell to %d chunks, under the floor %d", bitrate, chunks, listenerJitterFloor)
		}
	}
	// An unset bitrate falls back rather than trimming on every write.
	if got, want := jitterChunks(0), jitterChunks(defaultBitrateKbps); got != want {
		t.Errorf("an unset bitrate gave %d chunks, want the %dkbps default of %d", got, defaultBitrateKbps, want)
	}
}

// ---- what a skip is allowed to forget ----------------------------------

type recordingRecorder struct {
	mu        sync.Mutex
	discarded []string
}

func (r *recordingRecorder) OnPlayStart(string, PlaybackItem) (string, error) { return "log-1", nil }

func (r *recordingRecorder) OnPlayEnd(string, PlaybackItem, time.Duration, bool, string) {}

func (r *recordingRecorder) OnPlayDiscard(playLogID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.discarded = append(r.discarded, playLogID)
}

func (r *recordingRecorder) forgot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.discarded...)
}

// skippingStreamer is a streamer with one item "playing", ready to be skipped.
func skippingStreamer(t *testing.T, item PlaybackItem, recorder PlayRecorder) *channelStreamer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	streamer := newChannelStreamer(
		Channel{ID: "chan-test", Name: "Test", Codec: "mp3"},
		Dependencies{}, NewScheduler(Dependencies{}),
		StreamerOptions{Logger: log.New(io.Discard, "", 0), BaseContext: ctx},
		recorder,
	)
	t.Cleanup(func() { streamer.stopAndWait(context.Background()) })

	copyItem := item
	streamer.current = &copyItem
	streamer.currentLog = "log-1"
	streamer.currentAt = time.Now().UTC()
	streamer.skipMu.Lock()
	streamer.skipCancel = func() {}
	streamer.skipMu.Unlock()
	return streamer
}

// Skipping a song must not delete the record that it played.
//
// The play log is a freshness record for a podcast and a QUEUE for a playlist,
// and forgetting an airing means opposite things to the two. For a track it
// means the song is no longer merely fresh, it is unplayed — level with the
// records that have never come round, so it sits at the top of the eligible
// pile and returns the moment the 45-minute skip window lapses. Three times an
// hour, for ever, and every return is a slot the rest of the playlist does not
// get. Measured over two days of a 300-song playlist, the two skipped tracks
// were served thirteen and twelve times against two or three for everything
// else — so the one button that means "not this" was the only reliable way to
// hear something more.
func TestSkippingASongDoesNotForgetThatItPlayed(t *testing.T) {
	recorder := &recordingRecorder{}
	streamer := skippingStreamer(t, PlaybackItem{
		Title: "Saturday", ItemRef: "track:t7", SourceID: "mus1",
		Kind: SourceMusicPlaylist, Category: "music", Shuffled: true,
	}, recorder)

	if !streamer.skipCurrent() {
		t.Fatal("skip did not take")
	}
	if forgot := recorder.forgot(); len(forgot) > 0 {
		t.Fatalf("skipping a track forgot its airing (%v) — it rejoins the pool as never-played", forgot)
	}
}

// A podcast episode keeps the old answer: three seconds of audio must not cost
// you the episode, because for a strand the log is what freshness reads.
func TestSkippingAnEpisodeStillForgetsAGlancingAiring(t *testing.T) {
	recorder := &recordingRecorder{}
	streamer := skippingStreamer(t, PlaybackItem{
		Title: "Ep 12", ItemRef: "episode:e12", SourceID: "pod1",
		Kind: SourcePodcastSubscription, Category: "talk",
	}, recorder)

	if !streamer.skipCurrent() {
		t.Fatal("skip did not take")
	}
	if forgot := recorder.forgot(); len(forgot) != 1 {
		t.Fatalf("a glancing airing of an episode should be forgotten, discarded = %v", forgot)
	}
}
