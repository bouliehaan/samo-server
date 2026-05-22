package files

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestValidateLocalPathAllowsLibraryFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	libraryDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	audioPath := filepath.Join(libraryDir, "song.flac")
	if err := os.WriteFile(audioPath, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path)
		VALUES ('library-1', 'Music', 'music', '', ?)`, libraryDir); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	if _, _, err := service.ValidateLocalPath(ctx, audioPath); err != nil {
		t.Fatalf("ValidateLocalPath() error = %v, want nil", err)
	}
	if _, _, err := service.ValidateLocalPath(ctx, filepath.Join(root, "outside.flac")); err != ErrForbidden {
		t.Fatalf("outside path error = %v, want forbidden", err)
	}
}

func TestServeMediaFileSupportsRangeRequests(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	libraryDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	audioPath := filepath.Join(libraryDir, "song.flac")
	payload := []byte("0123456789")
	if err := os.WriteFile(audioPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path)
		VALUES ('library-1', 'Music', 'music', '', ?)`, libraryDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, path, file_name, mime_type, size_bytes, duration_seconds)
		VALUES ('file-1', 'library-1', ?, 'song.flac', 'audio/flac', ?, 1)`, audioPath, len(payload)); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/files/file-1/stream", nil)
	req.Header.Set("Range", "bytes=0-4")
	rec := httptest.NewRecorder()
	if err := service.ServeMediaFile(ctx, "file-1", rec, req); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusPartialContent && rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 206 or 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected streamed body")
	}
}
