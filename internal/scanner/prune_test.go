package scanner

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestPruneRemovesStaleMediaFilesAndOrphanTracks(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	scanner := New(db)
	library := Library{ID: "library-1", Name: "Music", Kind: "music", Path: "/music"}
	if err := scanner.upsertLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}

	artist := catalog.MusicArtist{ID: "artist-1", Name: "The Static"}
	if err := scanner.upsertMusicArtist(ctx, artist); err != nil {
		t.Fatal(err)
	}
	album := catalog.MusicAlbum{ID: "album-1", Title: "Night Broadcasts", DisplayArtist: "The Static"}
	if err := scanner.upsertMusicAlbum(ctx, album); err != nil {
		t.Fatal(err)
	}
	track := catalog.MusicTrack{ID: "track-1", Title: "Signal One", AlbumID: album.ID, DurationSeconds: 10}
	if err := scanner.upsertMusicTrack(ctx, track); err != nil {
		t.Fatal(err)
	}
	stalePath := "/music/stale.flac"
	if err := scanner.upsertAudioFile(ctx, library.ID, audioFileOwner{TrackID: track.ID}, catalog.AudioFile{
		ID:       "file-stale",
		Path:     stalePath,
		FileName: "stale.flac",
	}, "", ""); err != nil {
		t.Fatal(err)
	}

	accumulator := newScanAccumulator()
	accumulator.seeFile("/music/current.flac")
	stats, err := scanner.pruneLibrary(ctx, library, accumulator)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesMarked != 1 {
		t.Fatalf("files marked missing = %d, want 1", stats.FilesMarked)
	}
	if stats.FilesPruned != 0 {
		t.Fatalf("files pruned = %d, want 0", stats.FilesPruned)
	}

	var missing int
	if err := db.QueryRowContext(ctx, `SELECT missing FROM media_files WHERE path = ?`, stalePath).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != 1 {
		t.Fatalf("missing flag = %d, want 1", missing)
	}

	var trackCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_tracks WHERE id = ?`, track.ID).Scan(&trackCount); err != nil {
		t.Fatal(err)
	}
	if trackCount != 1 {
		t.Fatalf("track count = %d, want 1 while file is marked missing", trackCount)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM media_files WHERE path = ?`, stalePath); err != nil {
		t.Fatal(err)
	}
	orphanPruned, err := scanner.pruneOrphanMusic(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if orphanPruned != 3 {
		t.Fatalf("orphan rows pruned = %d, want 3 (track, album, artist)", orphanPruned)
	}

	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_tracks WHERE id = ?`, track.ID).Scan(&trackCount); err != nil {
		t.Fatal(err)
	}
	if trackCount != 0 {
		t.Fatalf("track count = %d, want 0 after manual removal", trackCount)
	}
}

// A playlist row holding an empty string in track_ids_json must not fail the
// orphan prune. The column is NOT NULL DEFAULT '[]', so that takes a bad write
// to produce — but casting an empty string to json raises, and the raise aborts
// the whole statement rather than skipping the one playlist, taking every other
// library's prune with it.
func TestPruneOrphanMusicSurvivesEmptyPlaylistTrackIDs(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	scanner := New(db)
	// The DELETE only evaluates its playlist subquery if there is something to
	// delete, so the orphan track is what makes this test exercise the cast.
	if err := scanner.upsertMusicTrack(ctx, catalog.MusicTrack{ID: "track-orphan", Title: "Unreferenced"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_playlists (id, name, track_ids_json)
		VALUES ('playlist-empty', 'Broken', '')`); err != nil {
		t.Fatal(err)
	}

	pruned, err := scanner.pruneOrphanMusic(ctx)
	if err != nil {
		t.Fatalf("prune with empty track_ids_json: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1 (the orphan track)", pruned)
	}
}

func TestScanWithStatsTracksSeenFiles(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	scanner := New(db)
	library := Library{ID: "library-1", Name: "Music", Kind: "music", Path: filepath.Clean("/music")}
	if err := scanner.upsertLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	accumulator := newScanAccumulator()
	accumulator.seeFile("/music/song.flac")
	scanner.activeScan = accumulator

	if err := scanner.upsertAudioFile(ctx, library.ID, audioFileOwner{}, catalog.AudioFile{
		ID:       "file-1",
		Path:     "/music/song.flac",
		FileName: "song.flac",
	}, "", ""); err != nil {
		t.Fatal(err)
	}
	if len(accumulator.filePaths) != 1 {
		t.Fatalf("seen files = %d, want 1", len(accumulator.filePaths))
	}
}
