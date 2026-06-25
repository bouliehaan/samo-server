package scanner

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestMoveMatchedTrackPreservesTrackID(t *testing.T) {
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
	libraryID := "lib_move"
	trackID := "track_stable"
	pid := "pid_abc"
	oldPath := "/music/old.flac"
	newPath := "/music/new.flac"

	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, updated_at)
		VALUES (?, 'Music', 'music', '', '/music', CURRENT_TIMESTAMP)`, libraryID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, updated_at)
		VALUES (?, 'Song', CURRENT_TIMESTAMP)`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, track_pid, content_hash, missing, updated_at)
		VALUES ('file_old', ?, ?, ?, 'old.flac', 'old.flac', ?, 'hash1', 1, CURRENT_TIMESTAMP)`,
		libraryID, trackID, oldPath, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, updated_at)
		VALUES ('track_orphan', 'Orphan', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, track_pid, content_hash, checksum, updated_at)
		VALUES ('file_new', ?, 'track_orphan', ?, 'new.flac', 'new.flac', ?, 'hash1', 'chk', CURRENT_TIMESTAMP)`,
		libraryID, newPath, pid); err != nil {
		t.Fatal(err)
	}

	if err := scanner.moveMatchedTrack(ctx, libraryID, indexedMediaFile{
		ID: "file_new", Path: newPath, TrackID: "track_orphan", TrackPID: pid, ContentHash: "hash1",
	}, indexedMediaFile{
		ID: "file_old", Path: oldPath, TrackID: trackID, TrackPID: pid, ContentHash: "hash1", Missing: true,
	}); err != nil {
		t.Fatalf("moveMatchedTrack: %v", err)
	}

	var path string
	var missing int
	if err := db.QueryRowContext(ctx, `SELECT path, missing FROM media_files WHERE id = 'file_old'`).Scan(&path, &missing); err != nil {
		t.Fatal(err)
	}
	if path != newPath || missing != 0 {
		t.Fatalf("file_old = path %q missing %d, want %q / 0", path, missing, newPath)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_files WHERE id = 'file_new'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("duplicate file_new row should be deleted")
	}
}
