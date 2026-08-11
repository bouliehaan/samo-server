package channels

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"

	"github.com/bouliehaan/samo-server/internal/loudness"
)

// stubPlanner stands in for internal/loudness so these tests need neither a
// database nor an ffmpeg.
type stubPlanner struct {
	plan  loudness.Plan
	found bool

	mu     sync.Mutex
	asked  []string
	warmed []string
}

func (s *stubPlanner) PlanFor(_ context.Context, req loudness.Request) (loudness.Plan, loudness.Measurement, bool) {
	s.mu.Lock()
	s.asked = append(s.asked, req.Input)
	s.mu.Unlock()
	return s.plan, loudness.Measurement{IntegratedLUFS: -22, TruePeakDBTP: -6}, s.found
}

func (s *stubPlanner) Warm(_ context.Context, req loudness.Request) {
	s.mu.Lock()
	s.warmed = append(s.warmed, req.Input)
	s.mu.Unlock()
}

func plannerStreamer(t *testing.T, planner LoudnessPlanner) *channelStreamer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return newChannelStreamer(
		Channel{ID: "chan-loud", Name: "Test", Codec: "mp3"},
		Dependencies{},
		NewScheduler(Dependencies{}),
		StreamerOptions{
			Logger:      log.New(io.Discard, "", 0),
			BaseContext: ctx,
			Loudness:    planner,
		},
		nil,
	)
}

func TestLoudnessFilterUsesCachedPlan(t *testing.T) {
	planner := &stubPlanner{plan: loudness.Plan{GainDB: -6.6}, found: true}
	streamer := plannerStreamer(t, planner)

	spec := streamer.loudnessFilter(context.Background(), PlaybackItem{
		URL: "/music/loud.flac", Title: "Loud", DurationSeconds: 210,
	})
	if spec != "volume=-6.6dB" {
		t.Fatalf("filter = %q, want the cached plan's gain", spec)
	}
	if len(planner.asked) != 1 || planner.asked[0] != "/music/loud.flac" {
		t.Fatalf("planner was asked about %v, want the item's own URL — measuring a\n"+
			"different rendition than the one that airs is how levels go wrong", planner.asked)
	}
}

// A cache miss must play the item untouched rather than stall the channel.
// Nothing in a live pipeline may wait on an analysis subprocess: dead air
// between every pair of items is a much worse fault than one item airing loud.
func TestLoudnessFilterIsEmptyOnCacheMiss(t *testing.T) {
	streamer := plannerStreamer(t, &stubPlanner{found: false})
	if spec := streamer.loudnessFilter(context.Background(), PlaybackItem{URL: "/music/new.flac"}); spec != "" {
		t.Fatalf("filter = %q, want no filter so the item plays immediately", spec)
	}
}

func TestLoudnessFilterOffWithoutAPlanner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	streamer := newChannelStreamer(
		Channel{ID: "chan-none", Codec: "mp3"},
		Dependencies{},
		NewScheduler(Dependencies{}),
		StreamerOptions{Logger: log.New(io.Discard, "", 0), BaseContext: ctx},
		nil,
	)
	if spec := streamer.loudnessFilter(context.Background(), PlaybackItem{URL: "/music/x.flac"}); spec != "" {
		t.Fatalf("filter = %q, want nothing when levelling is off", spec)
	}
}

// The filter has to land in the ffmpeg command line, not just be computed.
// This pins the position too: -af must come after -i, or ffmpeg applies it to
// the wrong side of the graph.
func TestTranscodeArgsCarryTheFilter(t *testing.T) {
	args := transcodeArgs(PlaybackItem{URL: "/music/quiet.flac"}, "mp3", "mp3", 192, 44100, "volume=8.0dB")

	afIndex, inputIndex := -1, -1
	for i, arg := range args {
		switch arg {
		case "-af":
			afIndex = i
		case "-i":
			inputIndex = i
		}
	}
	if afIndex < 0 {
		t.Fatalf("-af missing from %v", args)
	}
	if afIndex < inputIndex {
		t.Fatalf("-af at %d precedes -i at %d; the filter must apply to the decoded input", afIndex, inputIndex)
	}
	if args[afIndex+1] != "volume=8.0dB" {
		t.Fatalf("-af value = %q", args[afIndex+1])
	}
}

func TestTranscodeArgsOmitAnEmptyFilter(t *testing.T) {
	for _, arg := range transcodeArgs(PlaybackItem{URL: "/music/x.flac"}, "mp3", "mp3", 192, 44100, "") {
		if arg == "-af" {
			t.Fatal("an unlevelled item must get no -af at all, not an empty one")
		}
	}
}
