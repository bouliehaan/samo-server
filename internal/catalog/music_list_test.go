package catalog

import (
	"testing"
	"time"
)

func TestMusicListSortsByRecentAndTitle(t *testing.T) {
	older := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{
			{ID: "alpha", Title: "Alpha", AddedAt: &older},
			{ID: "zeta", Title: "Zeta", AddedAt: &newer},
		},
		MusicTracks: []MusicTrack{
			{ID: "one", Title: "One", AddedAt: &older},
			{ID: "two", Title: "Two", AddedAt: &newer},
		},
		MusicArtists: []MusicArtist{
			{ID: "artist-a", Name: "Alpha", AddedAt: &older},
			{ID: "artist-z", Name: "Zeta", AddedAt: &newer},
		},
	})

	recent := service.ListMusicAlbumsSorted(MusicListOptions{
		Page:      PageRequest{Limit: 10},
		Sort:      MusicListSortRecent,
		Direction: SortDirectionDesc,
	})
	if recent.Items[0].ID != "zeta" {
		t.Fatalf("recent album = %s, want zeta", recent.Items[0].ID)
	}

	titleDesc := service.ListMusicTracksSorted(MusicListOptions{
		Page:      PageRequest{Limit: 10},
		Sort:      MusicListSortAZ,
		Direction: SortDirectionDesc,
	})
	if titleDesc.Items[0].ID != "two" {
		t.Fatalf("title desc track = %s, want two", titleDesc.Items[0].ID)
	}

	artistsAsc := service.ListMusicArtistsSorted(MusicListOptions{
		Page:      PageRequest{Limit: 10},
		Sort:      MusicListSortRecent,
		Direction: SortDirectionAsc,
	})
	if artistsAsc.Items[0].ID != "artist-a" {
		t.Fatalf("recent asc artist = %s, want artist-a", artistsAsc.Items[0].ID)
	}
}

func TestMusicArtistListSortsByPlayCountWithOverlay(t *testing.T) {
	service := NewService(Seed{
		MusicArtists: []MusicArtist{
			{ID: "low", Name: "Alpha"},
			{ID: "high", Name: "Zeta"},
		},
	})

	page := service.ListMusicArtistsSorted(MusicListOptions{
		Page:      PageRequest{Limit: 10},
		Sort:      MusicListSortPlayCount,
		Direction: SortDirectionDesc,
		Playback: MusicListPlaybackOverlay{
			ArtistStates: map[string]PlaybackState{
				"low":  {PlayCount: 1},
				"high": {PlayCount: 99},
			},
		},
	})
	if page.Items[0].ID != "high" {
		t.Fatalf("top artist = %s, want high", page.Items[0].ID)
	}
}

func TestRollupTrackPlaybackToParentsAggregatesArtistPlays(t *testing.T) {
	playedAt := time.Now().UTC()
	tracks := []MusicTrack{{ID: "track-1", Title: "Hit", ArtistIDs: []string{"popular"}}}
	artistRollup, _ := rollupTrackPlaybackToParents(tracks, map[string]PlaybackState{
		"track-1": {PlayCount: 12, LastPlayedAt: &playedAt},
	})
	if artistRollup["popular"].PlayCount != 12 {
		t.Fatalf("rollup play count = %d, want 12", artistRollup["popular"].PlayCount)
	}
}

func TestArtistSortAfterTrackPlaybackOverlay(t *testing.T) {
	playedAt := time.Now().UTC()
	items := []MusicArtist{
		{ID: "quiet", Name: "Quiet"},
		{ID: "popular", Name: "Popular"},
	}
	tracks := []MusicTrack{{ID: "track-1", Title: "Hit", ArtistIDs: []string{"popular"}}}
	applyArtistPlaybackOverlay(items, nil, tracks, map[string]PlaybackState{
		"track-1": {PlayCount: 12, LastPlayedAt: &playedAt},
	})
	sortMusicArtistList(items, MusicListOptions{
		Sort:      MusicListSortPlayCount,
		Direction: SortDirectionDesc,
	})
	if items[0].ID != "popular" {
		t.Fatalf("sorted order = %#v", items)
	}
}

func TestMusicArtistListSortsByRolledUpTrackPlayCount(t *testing.T) {
	playedAt := time.Now().UTC()
	service := NewService(Seed{
		MusicArtists: []MusicArtist{
			{ID: "popular", Name: "Popular"},
			{ID: "quiet", Name: "Quiet"},
		},
		MusicTracks: []MusicTrack{
			{ID: "track-1", Title: "Hit", ArtistIDs: []string{"popular"}},
		},
	})

	page := service.ListMusicArtistsSorted(MusicListOptions{
		Page:      PageRequest{Limit: 10},
		Sort:      MusicListSortPlayCount,
		Direction: SortDirectionDesc,
		Playback: MusicListPlaybackOverlay{
			TrackStates: map[string]PlaybackState{
				"track-1": {PlayCount: 12, LastPlayedAt: &playedAt},
			},
		},
	})
	var popularItem *MusicArtist
	for index := range page.Items {
		if page.Items[index].ID == "popular" {
			popularItem = &page.Items[index]
			break
		}
	}
	if popularItem == nil {
		t.Fatalf("popular missing from %#v", page.Items)
	}
	if popularItem.Playback.PlayCount != 12 {
		t.Fatalf("popular playCount = %d, want 12", popularItem.Playback.PlayCount)
	}
	if page.Items[0].ID != "popular" {
		t.Fatalf("top artist = %s (playCount=%d), want popular (12); items=%#v", page.Items[0].ID, page.Items[0].Playback.PlayCount, page.Items)
	}
}

func TestMusicAlbumListSortsByReleaseDate(t *testing.T) {
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{
			{ID: "old", Title: "Old", ReleaseYear: 1999},
			{ID: "new", Title: "New", ReleaseDate: "2024-06-01"},
			{ID: "unknown", Title: "Unknown"},
		},
	})

	releases := service.ListMusicAlbumsSorted(MusicListOptions{
		Page:      PageRequest{Limit: 10},
		Sort:      MusicListSortRelease,
		Direction: SortDirectionDesc,
	})
	if releases.Items[0].ID != "new" {
		t.Fatalf("newest release = %s, want new", releases.Items[0].ID)
	}
	if releases.Items[1].ID != "old" {
		t.Fatalf("second release = %s, want old", releases.Items[1].ID)
	}
	if releases.Items[2].ID != "unknown" {
		t.Fatalf("last release = %s, want unknown", releases.Items[2].ID)
	}
}
