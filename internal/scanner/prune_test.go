package scanner

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestPruneRemovesStaleMediaFilesAndOrphanTracks(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

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
	}); err != nil {
		t.Fatal(err)
	}

	accumulator := newScanAccumulator()
	accumulator.seeFile("/music/current.flac")
	stats, err := scanner.pruneLibrary(ctx, library, accumulator)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesPruned != 1 {
		t.Fatalf("files pruned = %d, want 1", stats.FilesPruned)
	}
	if err := scanner.pruneOrphanMusic(ctx); err != nil {
		t.Fatal(err)
	}

	var trackCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_tracks WHERE id = ?`, track.ID).Scan(&trackCount); err != nil {
		t.Fatal(err)
	}
	if trackCount != 0 {
		t.Fatalf("track count = %d, want 0 after prune", trackCount)
	}
}

func TestScanWithStatsTracksSeenFiles(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

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
	}); err != nil {
		t.Fatal(err)
	}
	if len(accumulator.filePaths) != 1 {
		t.Fatalf("seen files = %d, want 1", len(accumulator.filePaths))
	}
}
