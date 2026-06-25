package lastfm

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

func TestShouldScrobbleUsesHalfDurationOrFourMinutes(t *testing.T) {
	tests := []struct {
		name     string
		progress int
		duration int
		want     bool
	}{
		{name: "short track half", progress: 60, duration: 120, want: true},
		{name: "short track too early", progress: 20, duration: 120, want: false},
		{name: "long track four minutes", progress: 240, duration: 600, want: true},
		{name: "long track before threshold", progress: 200, duration: 600, want: false},
		{name: "complete override", progress: 45, duration: 600, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			force := tc.name == "complete override"
			if got := shouldScrobble(tc.progress, tc.duration, force); got != tc.want {
				t.Fatalf("shouldScrobble(%d, %d, %v) = %v, want %v", tc.progress, tc.duration, force, got, tc.want)
			}
		})
	}
}

func TestShouldStartNewPlaySession(t *testing.T) {
	increment := playback.PatchInput{IncrementPlayCount: true}
	if !shouldStartNewPlaySession(catalogPlayback(120), catalogPlayback(5), &increment) {
		t.Fatal("expected incrementPlayCount to start a new session")
	}
	if !shouldStartNewPlaySession(catalogPlayback(90), catalogPlayback(10), nil) {
		t.Fatal("expected seek-back to start a new session")
	}
}

func TestShouldAbandonSessionOnSkip(t *testing.T) {
	skip := playback.PatchInput{IncrementSkipCount: true}
	if !shouldAbandonSession(catalogPlayback(10), catalogPlayback(10), &skip) {
		t.Fatal("expected skip to abandon session")
	}
}

func catalogPlayback(seconds int) catalog.PlaybackState {
	return catalog.PlaybackState{ProgressSeconds: seconds}
}
