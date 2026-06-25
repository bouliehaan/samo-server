package catalog

import (
	"testing"
	"time"
)

func TestEnrichAlbumAddedAtFromFiles(t *testing.T) {
	older := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	scanTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	albums := []MusicAlbum{
		{ID: "album-a", Title: "A", AddedAt: &scanTime},
	}
	tracks := []MusicTrack{
		{
			ID:      "track-1",
			AlbumID: "album-a",
			AudioFiles: []AudioFile{
				{ModifiedAt: &older},
				{ModifiedAt: &newer},
			},
		},
	}
	EnrichAlbumAddedAtFromFiles(albums, tracks)
	if albums[0].AddedAt == nil || !albums[0].AddedAt.Equal(newer) {
		t.Fatalf("addedAt = %v, want %v", albums[0].AddedAt, newer)
	}
}
