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
