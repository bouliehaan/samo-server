package files

import (
	"bytes"
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

func TestValidateLocalPathRejectsSymlinkEscape(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	libraryDir := filepath.Join(root, "music")
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(outsideDir, "secret.flac")
	if err := os.WriteFile(outsidePath, []byte("not library media"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(libraryDir, "linked.flac")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
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
	if _, _, err := service.ValidateLocalPath(ctx, linkPath); err != ErrForbidden {
		t.Fatalf("symlink escape error = %v, want forbidden", err)
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

func TestServeMediaFileReturnsExactSourceBytes(t *testing.T) {
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

	rec := httptest.NewRecorder()
	if err := service.ServeMediaFile(ctx, "file-1", rec, httptest.NewRequest(http.MethodGet, "/stream", nil)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), payload) {
		t.Fatalf("body = %q, want %q", rec.Body.Bytes(), payload)
	}

	rangeRec := httptest.NewRecorder()
	rangeReq := httptest.NewRequest(http.MethodGet, "/stream", nil)
	rangeReq.Header.Set("Range", "bytes=2-6")
	if err := service.ServeMediaFile(ctx, "file-1", rangeRec, rangeReq); err != nil {
		t.Fatal(err)
	}
	if rangeRec.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", rangeRec.Code)
	}
	wantRange := payload[2:7]
	if !bytes.Equal(rangeRec.Body.Bytes(), wantRange) {
		t.Fatalf("range body = %q, want %q", rangeRec.Body.Bytes(), wantRange)
	}
}

func TestServeMediaFileSetsDirectPlaybackHeaders(t *testing.T) {
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
		VALUES ('file-1', 'library-1', ?, 'song.flac', 'audio/flac', ?, 10)`, audioPath, len(payload)); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	rec := httptest.NewRecorder()
	if err := service.ServeMediaFile(ctx, "file-1", rec, httptest.NewRequest(http.MethodGet, "/stream", nil)); err != nil {
		t.Fatal(err)
	}
	if rec.Header().Get("Content-Type") != "audio/flac" {
		t.Fatalf("content type = %q, want audio/flac", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("accept-ranges = %q, want bytes", rec.Header().Get("Accept-Ranges"))
	}
	if rec.Header().Get("Cache-Control") != "private, max-age=3600" {
		t.Fatalf("cache-control = %q", rec.Header().Get("Cache-Control"))
	}
	if cl := rec.Header().Get("Content-Length"); cl != "10" {
		t.Fatalf("content-length = %q, want 10", cl)
	}
}

func TestServeMediaFileAtResumeSupportsRangeRequests(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	libraryDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	audioPath := filepath.Join(libraryDir, "song.flac")
	payload := []byte("0123456789abcdefghij")
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
		VALUES ('file-1', 'library-1', ?, 'song.flac', 'audio/flac', ?, 10)`, audioPath, len(payload)); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	rangeRec := httptest.NewRecorder()
	rangeReq := httptest.NewRequest(http.MethodGet, "/stream", nil)
	rangeReq.Header.Set("Range", "bytes=0-4")
	if err := service.ServeMediaFileAt(ctx, "file-1", 5, rangeRec, rangeReq); err != nil {
		t.Fatal(err)
	}
	if rangeRec.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", rangeRec.Code)
	}
	wantRange := payload[10:15]
	if !bytes.Equal(rangeRec.Body.Bytes(), wantRange) {
		t.Fatalf("range body = %q, want %q", rangeRec.Body.Bytes(), wantRange)
	}
}

func TestServeMediaFileAtResumeReturnsUnmodifiedTailBytes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	libraryDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	audioPath := filepath.Join(libraryDir, "song.flac")
	payload := []byte("0123456789abcdefghij")
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
		VALUES ('file-1', 'library-1', ?, 'song.flac', 'audio/flac', ?, 10)`, audioPath, len(payload)); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	rec := httptest.NewRecorder()
	// 50% through a 10-second file should start at byte 10 of 20.
	if err := service.ServeMediaFileAt(ctx, "file-1", 5, rec, httptest.NewRequest(http.MethodGet, "/stream", nil)); err != nil {
		t.Fatal(err)
	}
	want := payload[10:]
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("body = %q, want %q", rec.Body.Bytes(), want)
	}
	if got := rec.Header().Get("X-Samo-Stream-Offset-Seconds"); got != "5" {
		t.Fatalf("offset header = %q, want 5", got)
	}
}

func TestByteOffsetForSeconds(t *testing.T) {
	if got := byteOffsetForSeconds(100, 10, 5); got != 50 {
		t.Fatalf("offset = %d, want 50", got)
	}
	if got := byteOffsetForSeconds(100, 0, 5); got != 0 {
		t.Fatalf("offset = %d, want 0 when duration missing", got)
	}
}
