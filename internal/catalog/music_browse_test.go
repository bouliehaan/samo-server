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
	if _, err := ParseMusicBrowseView("unplayed"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseMusicBrowseView("discovery"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseMusicBrowseView("unknown"); err == nil {
		t.Fatal("expected invalid browse view")
	}
}

func TestPickDiscoveryTracksMixesRecentAndOlder(t *testing.T) {
	now := time.Now().UTC()
	older := now.Add(-48 * time.Hour)
	tracks := []MusicTrack{
		{ID: "new-1", Title: "New 1", AddedAt: &now},
		{ID: "new-2", Title: "New 2", AddedAt: &now},
		{ID: "new-3", Title: "New 3", AddedAt: &now},
		{ID: "old-1", Title: "Old 1", AddedAt: &older},
		{ID: "old-2", Title: "Old 2", AddedAt: &older},
		{ID: "old-3", Title: "Old 3", AddedAt: &older},
		{ID: "old-4", Title: "Old 4", AddedAt: &older},
	}

	picked := PickDiscoveryTracks(tracks, 10)
	if len(picked) != 7 {
		t.Fatalf("picked len = %d, want 7", len(picked))
	}

	recentCount := 0
	olderCount := 0
	for _, track := range picked {
		switch track.ID {
		case "new-1", "new-2", "new-3":
			recentCount++
		case "old-1", "old-2", "old-3", "old-4":
			olderCount++
		}
	}
	if recentCount != 3 || olderCount != 4 {
		t.Fatalf("recent=%d older=%d, want 3 recent and 4 older in %#v", recentCount, olderCount, picked)
	}
}

func TestMusicBrowseDiscoveryReturnsTracksOnly(t *testing.T) {
	now := time.Now().UTC()
	older := now.Add(-72 * time.Hour)
	service := NewService(Seed{
		MusicArtists: []MusicArtist{{ID: "artist-1", Name: "Artist"}},
		MusicAlbums:  []MusicAlbum{{ID: "album-1", Title: "Album"}},
		MusicTracks: []MusicTrack{
			{ID: "heard", Title: "Heard", AddedAt: &now},
			{ID: "fresh", Title: "Fresh", AddedAt: &now},
			{ID: "deep", Title: "Deep", AddedAt: &older},
		},
	})

	results := service.MusicBrowse(
		map[string]PlaybackState{
			"heard": {PlayCount: 2},
			"fresh": {PlayCount: 0},
			"deep":  {PlayCount: 0},
		},
		nil, nil, nil,
		MusicBrowseDiscovery,
		PageRequest{Limit: 10},
	)
	if len(results.Artists) != 0 || len(results.Albums) != 0 {
		t.Fatalf("discovery should be tracks-only, got artists=%d albums=%d", len(results.Artists), len(results.Albums))
	}
	if len(results.Tracks) != 2 {
		t.Fatalf("tracks = %#v", results.Tracks)
	}
}

func TestMusicBrowseUnplayedFiltersZeroPlayCount(t *testing.T) {
	service := NewService(Seed{
		MusicTracks: []MusicTrack{
			{ID: "heard", Title: "Heard"},
			{ID: "fresh", Title: "Fresh"},
		},
	})

	results := service.MusicBrowse(
		map[string]PlaybackState{
			"heard": {PlayCount: 3},
			"fresh": {PlayCount: 0},
		},
		nil, nil, nil,
		MusicBrowseUnplayed,
		PageRequest{Limit: 10},
	)
	if len(results.Tracks) != 1 || results.Tracks[0].ID != "fresh" {
		t.Fatalf("unplayed tracks = %#v", results.Tracks)
	}
}

func TestMusicBrowseForUserFiltersPrivatePlaylists(t *testing.T) {
	service := NewService(Seed{
		MusicPlaylists: []MusicPlaylist{
			{ID: "private-other", Name: "Private Other", OwnerID: "user-other"},
			{ID: "public-other", Name: "Public Other", OwnerID: "user-other", Public: true},
			{ID: "private-owned", Name: "Private Owned", OwnerID: "user-me"},
		},
	})

	results := service.MusicBrowseForUser(
		nil, nil, nil,
		map[string]PlaybackState{
			"private-other": {Favorite: true},
			"public-other":  {Favorite: true},
			"private-owned": {Favorite: true},
		},
		MusicBrowseFavorites,
		PageRequest{Limit: 10},
		"user-me",
	)
	if len(results.Playlists) != 2 || results.Total != 2 {
		t.Fatalf("results = %#v", results)
	}
	for _, item := range results.Playlists {
		if item.ID == "private-other" {
			t.Fatalf("private playlist leaked: %#v", results.Playlists)
		}
	}
}
