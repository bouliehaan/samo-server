package channels

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"strings"
	"testing"
	"time"
)

// The wall shows when what is on gives way — and it could only ever work that
// out for a measured item, from startedAt plus durationSeconds. Everything
// else the station knew and kept to itself: a booked relay ends when its slot
// does, a station picked by the rotation gets a turn of so many minutes, an
// episode nobody measured is capped to the room in front of the next show,
// and an appointment cuts in on whatever is playing. MaxDuration never left
// the server, so the card had no end for any of those. Now-playing says it.
func TestItemEndsAtIsTheEarliestBoundTheStationKnows(t *testing.T) {
	start := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return start.Add(d) }

	for _, tc := range []struct {
		name  string
		item  PlaybackItem
		cutIn time.Time
		want  time.Time
		known bool
	}{
		{
			name:  "a measured episode in an open stretch ends when its audio does",
			item:  PlaybackItem{DurationSeconds: 13149},
			want:  at(13149 * time.Second),
			known: true,
		},
		{
			name:  "a booked live relay ends when its slot does",
			item:  PlaybackItem{Live: true, IsRuleDriven: true, MaxDuration: time.Hour},
			want:  at(time.Hour),
			known: true,
		},
		{
			name:  "a station the rotation picked ends when its turn does",
			item:  PlaybackItem{Live: true, MaxDuration: 30 * time.Minute},
			want:  at(30 * time.Minute),
			known: true,
		},
		{
			name:  "an unmeasured episode capped to the room before a show ends by the cap",
			item:  PlaybackItem{DurationSeconds: 0, MaxDuration: 38 * time.Minute},
			want:  at(38 * time.Minute),
			known: true,
		},
		{
			name:  "an unmeasured episode nothing bounds has no end to name",
			item:  PlaybackItem{DurationSeconds: 0},
			known: false,
		},
		{
			name:  "a song filling the gap in front of an appointment ends at the boundary",
			item:  PlaybackItem{DurationSeconds: 240, MaxDuration: 90 * time.Second, FadeOut: boundaryFade},
			want:  at(90 * time.Second),
			known: true,
		},
		{
			name:  "an appointment that will cut in is the end of what it cuts",
			item:  PlaybackItem{DurationSeconds: 3 * 3600},
			cutIn: at(25 * time.Minute),
			want:  at(25 * time.Minute),
			known: true,
		},
		{
			name:  "an appointment after the item's own end is not its end",
			item:  PlaybackItem{DurationSeconds: 20 * 60},
			cutIn: at(25 * time.Minute),
			want:  at(20 * time.Minute),
			known: true,
		},
		{
			name:  "a booked item is capped by its own slot and no appointment cuts it",
			item:  PlaybackItem{Live: true, IsRuleDriven: true, MaxDuration: time.Hour},
			cutIn: at(30 * time.Minute),
			want:  at(time.Hour),
			known: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, known := itemEndsAt(tc.item, start, tc.cutIn)
			if known != tc.known {
				t.Fatalf("known=%v, want %v (got %s)", known, tc.known, got.Format(time.Kitchen))
			}
			if known && !got.Equal(tc.want) {
				t.Fatalf("ends at %s, want %s", got.Format("15:04:05"), tc.want.Format("15:04:05"))
			}
		})
	}
}

// The end now-playing reports is the one the streamer's own clocks are set to.
func TestNowPlayingReportsTheEndTheStreamerSetItsClocksTo(t *testing.T) {
	streamer := skippingStreamer(t, PlaybackItem{
		Title: "KRCC", ItemRef: "station:krcc", Live: true, IsRuleDriven: true,
		MaxDuration: time.Hour,
	}, &recordingRecorder{})

	ends, known := streamer.EndsAt()
	if !known {
		t.Fatal("a booked relay has an end — its slot's — and now-playing must say so")
	}
	if want := streamer.currentAt.Add(time.Hour); !ends.Equal(want) {
		t.Fatalf("ends at %s, want the slot's end %s", ends.Format("15:04:05"), want.Format("15:04:05"))
	}

	// Nothing on: nothing ends.
	streamer.currentMu.Lock()
	streamer.current = nil
	streamer.currentMu.Unlock()
	if _, known := streamer.EndsAt(); known {
		t.Fatal("with nothing on air there is no end to report")
	}
}

// The appointment the streamer read when the item started is the one its cut
// timer fires on, and the one now-playing reports — not a fresh reading, which
// could disagree with the timer, and not the previous item's.
func TestTheEndFollowsTheCutInTheItemWasStartedUnder(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustChannel(t, db, "ch1")
	talk := mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourceLiveStream, Label: "Talk", Role: RoleTalk,
		Config: map[string]any{"url": "http://example.test/talk"}, Enabled: boolPtr(true),
	})
	// The cut timers are armed off the wall clock, so the appointment has to
	// be in the real future: eighteen minutes from now, on the channel's own
	// (fallback, UTC) clock.
	now := time.Now().UTC().Truncate(time.Minute)
	cutAt := now.Add(18 * time.Minute)
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{talk.ID}}},
		Blocks: []Block{
			{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "atc", Label: "All Things Considered",
				Enter: BlockEntry{At: cutAt.Format("15:04"), Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: cutAt.Add(time.Hour).Format("15:04")},
				Pools: []PoolRef{{Pool: "talk"}}},
		},
	}
	if err := SavePlan(ctx, db, "ch1", plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}

	deps := Dependencies{DB: db, Now: time.Now}
	loopCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	streamer := newChannelStreamer(
		Channel{ID: "ch1", Name: "Test", Codec: "mp3"},
		deps, NewScheduler(deps),
		StreamerOptions{
			FFmpegPath:  fakeTranscoder(t, `exec cat /dev/zero`),
			Logger:      log.New(io.Discard, "", 0),
			BaseContext: loopCtx,
		},
		nil,
	)
	t.Cleanup(func() { streamer.stopAndWait(context.Background()) })

	// What the loop does before it plays: the item goes up as current, with
	// the previous item's appointment forgotten.
	episode := PlaybackItem{Title: "#2554", ItemRef: "episode:e2554", URL: "/audio/e2554.mp3",
		DurationSeconds: 3 * 3600}
	streamer.currentMu.Lock()
	streamer.current = &episode
	streamer.currentAt = now
	streamer.currentCutIn = now.Add(-time.Hour) // a stale reading from the item before
	streamer.currentMu.Unlock()

	itemCtx, itemCancel := context.WithCancel(context.Background())
	defer itemCancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = streamer.playItem(itemCtx, episode)
	}()
	waitForDecodedAudio(t, streamer)

	ends, known := streamer.EndsAt()
	if !known {
		t.Fatal("a three-hour episode eighteen minutes before a startImmediately slot has an end")
	}
	if !ends.Equal(cutAt) {
		t.Fatalf("ends at %s, want the appointment at %s (the episode's own end is %s)",
			ends.Format("15:04:05"), cutAt.Format("15:04:05"), now.Add(3*time.Hour).Format("15:04:05"))
	}

	itemCancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("playItem did not return after the cancel")
	}
}

// The wall reads the end by name. A samo that spells it differently is a samo
// that says nothing, as far as the card is concerned.
func TestNowPlayingSpellsTheEndAsEndsAt(t *testing.T) {
	ends := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	body, err := json.Marshal(NowPlaying{ChannelID: "ch1", EndsAt: &ends})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"endsAt":"2026-09-18T09:00:00Z"`) {
		t.Fatalf("now-playing does not carry endsAt as the wall reads it: %s", body)
	}
	bare, err := json.Marshal(NowPlaying{ChannelID: "ch1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bare), "endsAt") {
		t.Fatalf("an unknown end must be absent, not null or zero: %s", bare)
	}
}
