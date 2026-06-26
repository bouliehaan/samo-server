package catalog

import (
	"testing"
	"time"
)

// "Recently Added" must order albums by the persisted music_albums.added_at (set
// once at first scan), NOT by the newest track's filesystem mtime. The previous
// behavior recomputed AddedAt live from file mtime on every catalog build, so a
// copy / restore / rsync-without-times that re-stamped old files pushed long-owned
// albums straight to the top of Recently Added. This locks the album's AddedAt to
// its persisted value regardless of how recent its files look on disk.
func TestAlbumAddedAtIsNotRecomputedFromFileMtime(t *testing.T) {
	firstScanned := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	recentlyTouchedFile := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{{ID: "album-1", Title: "Old Album", AddedAt: &firstScanned}},
		MusicTracks: []MusicTrack{{
			ID:         "track-1",
			AlbumID:    "album-1",
			AudioFiles: []AudioFile{{ID: "f1", ModifiedAt: &recentlyTouchedFile}},
		}},
	})

	album, err := service.MusicAlbum("album-1")
	if err != nil {
		t.Fatal(err)
	}
	if album.AddedAt == nil {
		t.Fatal("album AddedAt is nil; want the persisted first-scan timestamp")
	}
	if !album.AddedAt.Equal(firstScanned) {
		t.Fatalf("album AddedAt = %s, want persisted %s (must not follow the recent file mtime %s)",
			album.AddedAt.UTC(), firstScanned, recentlyTouchedFile)
	}
}
