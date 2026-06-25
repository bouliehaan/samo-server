package playlists

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestPlaylistCreateUpdateDelete(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, duration_seconds)
		VALUES ('track-1', 'One', 120), ('track-2', 'Two', 180)`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	created, err := service.Create(ctx, "user-1", CreateInput{
		Name:     "Night Mix",
		TrackIDs: []string{"track-1", "track-2"},
		Public:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.TrackCount != 2 || created.DurationSeconds != 300 {
		t.Fatalf("created = %#v", created)
	}

	name := "Night Mix v2"
	updated, err := service.Update(ctx, "user-1", created.ID, UpdateInput{
		Name:     &name,
		TrackIDs: []string{"track-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != name || updated.TrackCount != 1 || updated.DurationSeconds != 180 {
		t.Fatalf("updated = %#v", updated)
	}

	if err := service.Delete(ctx, "user-1", created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.loadByID(ctx, created.ID); err != ErrNotFound {
		t.Fatalf("delete err = %v", err)
	}
}

func TestPlaylistUpdateRejectsOtherOwner(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	created, err := service.Create(ctx, "user-1", CreateInput{Name: "Private Mix"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, "user-2", created.ID, UpdateInput{}); err != ErrForbidden {
		t.Fatalf("err = %v, want forbidden", err)
	}
}

func TestPlaylistImportCSVMatchesLocalTracks(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, display_artist, album_title, duration_seconds)
		VALUES
		  ('track-1', 'One More Time', 'Daft Punk', 'Discovery', 320),
		  ('track-2', 'Harder Better Faster Stronger', 'Daft Punk', 'Discovery', 224)`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	result, err := service.Import(ctx, "user-1", ImportInput{
		Name:       "Robots",
		SourceType: "csv",
		Content:    "title,artist,album,duration\nOne More Time,Daft Punk,Discovery,5:20\nMissing Song,Daft Punk,Discovery,3:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Playlist == nil || result.Playlist.TrackCount != 1 {
		t.Fatalf("playlist = %#v", result.Playlist)
	}
	if result.MatchedCount != 1 || result.UnmatchedCount != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result.TrackIDs[0] != "track-1" {
		t.Fatalf("track ids = %#v", result.TrackIDs)
	}
}

func TestPlaylistImportM3UMatchesByPath(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path)
		VALUES ('lib-1', 'Music', 'music', '/music');
		INSERT INTO music_tracks (id, title, display_artist, duration_seconds)
		VALUES ('track-1', 'Windowlicker', 'Aphex Twin', 364);
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds)
		VALUES ('file-1', 'lib-1', 'track-1', '/music/Aphex Twin/Windowlicker.flac', 'Aphex Twin/Windowlicker.flac', 'Windowlicker.flac', 364);`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	result, err := service.Import(ctx, "user-1", ImportInput{
		Name:       "M3U",
		SourceType: "m3u",
		Content:    "#EXTM3U\n#EXTINF:364,Aphex Twin - Windowlicker\n/music/Aphex Twin/Windowlicker.flac\n",
		DryRun:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Playlist != nil {
		t.Fatalf("dry run playlist = %#v", result.Playlist)
	}
	if result.MatchedCount != 1 || len(result.TrackIDs) != 1 || result.TrackIDs[0] != "track-1" {
		t.Fatalf("result = %#v", result)
	}
}
