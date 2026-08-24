package channels

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

type airingCall struct {
	episodeID string
	progress  int
	completed bool
}

type fakeAirings struct {
	mu    sync.Mutex
	calls []airingCall
}

func (f *fakeAirings) RecordEpisodeAiring(
	_ context.Context, episodeID string, progressSeconds int, completed bool,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, airingCall{episodeID, progressSeconds, completed})
	return nil
}

func (f *fakeAirings) recorded() []airingCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]airingCall(nil), f.calls...)
}

// Only an episode has playback state to write. Everything else on a channel —
// a track out of a playlist, a file off disk, a relayed station — shares the
// PlaybackItem shape but has no episode id hiding in its ref.
func TestEpisodeIDOfOnlyAnswersForEpisodes(t *testing.T) {
	cases := []struct {
		name string
		item PlaybackItem
		want string
	}{
		{"episode", PlaybackItem{Kind: SourcePodcastSubscription, ItemRef: "episode:e12"}, "e12"},
		{"track", PlaybackItem{Kind: SourceMusicPlaylist, ItemRef: "track:t7"}, ""},
		{"station", PlaybackItem{Kind: SourceInternetStation, ItemRef: "station:s1"}, ""},
		// A podcast whose ref lost its namespace is not an id to guess at.
		{"unprefixed", PlaybackItem{Kind: SourcePodcastSubscription, ItemRef: "e12"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := episodeIDOf(tc.item); got != tc.want {
				t.Fatalf("episodeIDOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// An episode the station played all the way through is one the station has
// heard, and the already-heard gate reads playback state to find that out.
func TestACompletedAiringIsRecorded(t *testing.T) {
	airings := &fakeAirings{}
	recorder := &serviceRecorder{
		baseCtx: context.Background(),
		logger:  log.New(io.Discard, "", 0), airings: airings,
	}

	recorder.OnPlayEnd("ch1", PlaybackItem{
		Title: "Ep 12", ItemRef: "episode:e12", SourceID: "pod1",
		Kind: SourcePodcastSubscription, DurationSeconds: 1800,
	}, 1800*time.Second, true, "")

	got := airings.recorded()
	if len(got) != 1 {
		t.Fatalf("a completed airing was not recorded, calls = %v", got)
	}
	if got[0].episodeID != "e12" || !got[0].completed {
		t.Fatalf("recorded %+v, want e12 complete", got[0])
	}
}

// An airing cut short is still owed. Obligations already track that
// proportionally, and writing progress for it would retire a three-hour episode
// on the two minutes that went out before a booked show took the slot.
func TestAnAiringCutShortIsNotRecorded(t *testing.T) {
	airings := &fakeAirings{}
	recorder := &serviceRecorder{
		baseCtx: context.Background(),
		logger:  log.New(io.Discard, "", 0), airings: airings,
	}

	recorder.OnPlayEnd("ch1", PlaybackItem{
		Title: "Ep 12", ItemRef: "episode:e12", SourceID: "pod1",
		Kind: SourcePodcastSubscription, DurationSeconds: 10800,
	}, 2*time.Minute, false, "")

	if got := airings.recorded(); len(got) > 0 {
		t.Fatalf("a preempted airing was recorded as heard: %v", got)
	}
}

// The clamp matters because `played` is wall clock. A stream that ran long
// against a duration the feed under-reported must not write progress past the
// end of the episode.
func TestARecordedAiringIsClampedToTheEpisode(t *testing.T) {
	airings := &fakeAirings{}
	recorder := &serviceRecorder{
		baseCtx: context.Background(),
		logger:  log.New(io.Discard, "", 0), airings: airings,
	}

	recorder.OnPlayEnd("ch1", PlaybackItem{
		ItemRef: "episode:e12", Kind: SourcePodcastSubscription, DurationSeconds: 600,
	}, 900*time.Second, true, "")

	got := airings.recorded()
	if len(got) != 1 || got[0].progress != 600 {
		t.Fatalf("recorded %v, want progress clamped to 600", got)
	}
}

// A skip retires the episode outright, and has to do it without leaning on the
// duration: feed episodes routinely report none, and a fraction of an unknown
// length clears no threshold.
func TestASkipRetiresAnEpisodeOfUnknownLength(t *testing.T) {
	airings := &fakeAirings{}
	service := &Service{airings: airings, logger: log.New(io.Discard, "", 0)}

	service.markSkipHeard(context.Background(), PlaybackItem{
		Title: "Ep 12", ItemRef: "episode:e12", SourceID: "pod1",
		Kind: SourcePodcastSubscription, DurationSeconds: 0,
	})

	got := airings.recorded()
	if len(got) != 1 {
		t.Fatalf("a skip did not retire the episode, calls = %v", got)
	}
	if !got[0].completed {
		t.Fatalf("recorded %+v, want it marked complete", got[0])
	}
}

// Skipping a song must not write podcast playback state for it.
func TestASkippedTrackRecordsNoAiring(t *testing.T) {
	airings := &fakeAirings{}
	service := &Service{airings: airings, logger: log.New(io.Discard, "", 0)}

	service.markSkipHeard(context.Background(), PlaybackItem{
		Title: "Saturday", ItemRef: "track:t7", Kind: SourceMusicPlaylist,
	})

	if got := airings.recorded(); len(got) > 0 {
		t.Fatalf("skipping a track wrote episode playback state: %v", got)
	}
}
