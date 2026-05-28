package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/users"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestStreamAudiobookSelectsFileFromPlaybackProgress(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bookDir := filepath.Join(root, "books", "Signal Manual")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}

	part1 := filepath.Join(bookDir, "01-opening.mp3")
	part2 := filepath.Join(bookDir, "02-middle.mp3")
	if err := os.WriteFile(part1, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part2, []byte("abcdefghij"), 0o644); err != nil {
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

	libraryID := "library-books"
	itemID := "item-signal"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path)
		VALUES (?, 'Books', 'audiobook', ?)`,
		libraryID, filepath.Join(root, "books")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO audiobooks (id, library_id, path, duration_seconds, progress_json)
		VALUES (?, ?, ?, 15, '{}')`,
		itemID, libraryID, bookDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO user_playback (user_id, target_kind, target_id, state_json, updated_at)
		VALUES (?, 'audiobook', ?, ?, datetime('now'))`,
		users.BootstrapUserID, itemID, `{"progressSeconds":12}`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id, path string
		duration int
	}{
		{"file-1", part1, 10},
		{"file-2", part2, 5},
	} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO media_files (id, library_id, audiobook_id, path, relative_path, file_name, mime_type, size_bytes, duration_seconds)
			VALUES (?, ?, ?, ?, ?, ?, 'audio/mpeg', ?, ?)`,
			row.id, libraryID, itemID, row.path, filepath.Base(row.path), filepath.Base(row.path), 10, row.duration); err != nil {
			t.Fatal(err)
		}
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	catalogService := catalog.NewService(seed)
	playbackService := playback.New(db)
	filesService := files.New(db)
	handler := NewServer(ServerOptions{
		Catalog:  catalogService,
		Playback: playbackService,
		Files:    filesService,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audiobooks/"+itemID+"/stream", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK && rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Samo-Media-File-Id"); got != "file-2" {
		t.Fatalf("media file header = %q, want file-2", got)
	}
	if got := rec.Header().Get("X-Samo-Stream-Offset-Seconds"); got != "2" {
		t.Fatalf("offset header = %q, want 2", got)
	}
	part2Payload := []byte("abcdefghij")
	if !bytes.Equal(rec.Body.Bytes(), part2Payload[4:]) {
		t.Fatalf("body = %q, want tail of part-2 from resume offset", rec.Body.Bytes())
	}
}

func TestStreamMediaFileReturnsExactSourceBytes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	libraryDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("0123456789")
	audioPath := filepath.Join(libraryDir, "song.flac")
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

	handler := NewServer(ServerOptions{Files: files.New(db)})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/files/file-1/stream", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), payload) {
		t.Fatalf("body = %q, want exact source bytes", rec.Body.Bytes())
	}
}
