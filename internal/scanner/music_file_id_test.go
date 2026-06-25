package scanner

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestMusicRescanSamePathDifferentTrackPIDUpdatesRow(t *testing.T) {
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
	library := Library{ID: "lib-music", Name: "Music", Kind: "music", Path: "/mnt/data2tb/Music"}
	if err := scanner.upsertLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}

	const path = "/mnt/data2tb/Music/In the Shadow of the Valley.m4a"
	album := catalog.MusicAlbum{ID: "album-1", Title: "Valley"}
	track := catalog.MusicTrack{ID: "track-1", Title: "In the Shadow of the Valley", AlbumID: album.ID}
	if err := scanner.upsertMusicAlbum(ctx, album); err != nil {
		t.Fatal(err)
	}
	if err := scanner.upsertMusicTrack(ctx, track); err != nil {
		t.Fatal(err)
	}

	file := catalog.AudioFile{
		ID:       stableID("file", path),
		Path:     path,
		FileName: "In the Shadow of the Valley.m4a",
	}
	if err := scanner.upsertAudioFile(ctx, library.ID, audioFileOwner{TrackID: track.ID}, file, "pid-old", "hash-old"); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Simulate album/PID logic change on full rescan — path unchanged.
	if err := scanner.upsertAudioFile(ctx, library.ID, audioFileOwner{TrackID: track.ID}, file, "pid-new", "hash-new"); err != nil {
		t.Fatalf("rescan upsert: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_files WHERE path = ?`, path).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("media_files rows for path = %d, want 1", count)
	}
	var pid string
	if err := db.QueryRowContext(ctx, `SELECT track_pid FROM media_files WHERE path = ?`, path).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if pid != "pid-new" {
		t.Fatalf("track_pid = %q, want pid-new", pid)
	}
}
