package catalog

import (
	"testing"
	"time"
)

func TestMusicBrowseFavoritesUsesUserPlaybackOverlay(t *testing.T) {
	now := time.Now().UTC()
	earlier := now.Add(-time.Hour)
	service := NewService(Seed{
		MusicTracks: []MusicTrack{
			{ID: "track-1", Title: "Alpha"},
			{ID: "track-2", Title: "Beta"},
		},
	})

	results := service.MusicBrowse(
		map[string]PlaybackState{"track-2": {Favorite: true, UserID: "user-1"}},
		nil,
		nil,
		nil,
		MusicBrowseFavorites,
		PageRequest{Limit: 10},
	)
	if len(results.Tracks) != 1 || results.Tracks[0].ID != "track-2" {
		t.Fatalf("tracks = %#v", results.Tracks)
	}
	if results.Total != 1 {
		t.Fatalf("total = %d, want 1", results.Total)
	}

	recent := service.MusicBrowse(
		map[string]PlaybackState{
			"track-1": {LastPlayedAt: &earlier},
			"track-2": {LastPlayedAt: &now},
		},
		nil, nil, nil,
		MusicBrowseRecentlyPlayed,
		PageRequest{Limit: 10},
	)
	if len(recent.Tracks) != 2 || recent.Tracks[0].ID != "track-2" {
		t.Fatalf("recent tracks = %#v", recent.Tracks)
	}
}

func TestParseMusicBrowseView(t *testing.T) {
	if _, err := ParseMusicBrowseView("favorites"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseMusicBrowseView("unknown"); err == nil {
		t.Fatal("expected invalid browse view")
	}
}
