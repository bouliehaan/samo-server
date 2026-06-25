package scanner

import (
	"context"
	"database/sql"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestUpsertAudioFileReclaimsPathFromMusicTrack(t *testing.T) {
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
	musicLib := Library{ID: "lib-music", Name: "Music", Kind: "music", Path: "/mnt/data2tb/Music"}
	bookLib := Library{ID: "lib-books", Name: "Books", Kind: "audiobook", Path: "/mnt/data2tb/Books"}
	for _, lib := range []Library{musicLib, bookLib} {
		if err := scanner.upsertLibrary(ctx, lib); err != nil {
			t.Fatal(err)
		}
	}

	const path = "/mnt/data2tb/Music/In the Shadow of the Valley.m4a"
	album := catalog.MusicAlbum{ID: "album-1", Title: "Valley"}
	track := catalog.MusicTrack{ID: "track-1", Title: "In the Shadow of the Valley", AlbumID: album.ID, DurationSeconds: 100}
	if err := scanner.upsertMusicAlbum(ctx, album); err != nil {
		t.Fatal(err)
	}
	if err := scanner.upsertMusicTrack(ctx, track); err != nil {
		t.Fatal(err)
	}
	if err := scanner.upsertAudioFile(ctx, musicLib.ID, audioFileOwner{TrackID: track.ID}, catalog.AudioFile{
		ID:              stableID("file", path),
		Path:            path,
		FileName:        "In the Shadow of the Valley.m4a",
		DurationSeconds: 100,
	}, "pid-1", "hash-1"); err != nil {
		t.Fatalf("seed music media file: %v", err)
	}

	book := catalog.AudiobookItem{
		ID:        "audiobook-1",
		LibraryID: bookLib.ID,
		Path:      "/mnt/data2tb/Music",
		Book:      &catalog.BookMetadata{Title: "In the Shadow of the Valley"},
	}
	if _, err := scanner.upsertAudiobook(ctx, book); err != nil {
		t.Fatal(err)
	}
	if err := scanner.upsertAudioFile(ctx, bookLib.ID, audioFileOwner{AudiobookID: book.ID}, catalog.AudioFile{
		ID:              stableID("file", path),
		Path:            path,
		FileName:        "In the Shadow of the Valley.m4a",
		DurationSeconds: 3600,
	}, "", ""); err != nil {
		t.Fatalf("reclaim for audiobook: %v", err)
	}

	var trackID, audiobookID sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT track_id, audiobook_id FROM media_files WHERE path = ?`, path,
	).Scan(&trackID, &audiobookID); err != nil {
		t.Fatal(err)
	}
	if trackID.Valid && trackID.String != "" {
		t.Fatalf("track_id = %q, want cleared", trackID.String)
	}
	if !audiobookID.Valid || audiobookID.String != book.ID {
		t.Fatalf("audiobook_id = %v, want %q", audiobookID, book.ID)
	}
	var trackRows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_tracks WHERE id = ?`, track.ID).Scan(&trackRows); err != nil {
		t.Fatal(err)
	}
	if trackRows != 0 {
		t.Fatalf("orphan track still exists")
	}
}
