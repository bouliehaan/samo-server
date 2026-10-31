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
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
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

	db := storagetest.Open(t)

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
		VALUES (?, 'audiobook', ?, ?, CURRENT_TIMESTAMP)`,
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

	seed, err := catalogstore.LoadSeedFromDB(ctx, db)
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
	// Book-global progress (12s) lands inside file-2's span [10s,15s), so file-2
	// is the file to serve.
	if got := rec.Header().Get("X-Samo-Media-File-Id"); got != "file-2" {
		t.Fatalf("media file header = %q, want file-2", got)
	}
	// New contract: the file is served WHOLE (no time->byte slicing). The client
	// seeks locally; we only tell it where the file starts on the book timeline.
	if got := rec.Header().Get("X-Samo-Stream-Offset-Seconds"); got != "" {
		t.Fatalf("offset header = %q, want empty (whole-file serving)", got)
	}
	if got := rec.Header().Get("X-Samo-Stream-File-Offset-Ms"); got != "10000" {
		t.Fatalf("file-offset header = %q, want 10000 (file-2 starts at 10s)", got)
	}
	part2Payload := []byte("abcdefghij")
	if !bytes.Equal(rec.Body.Bytes(), part2Payload) {
		t.Fatalf("body = %q, want the whole of part-2", rec.Body.Bytes())
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

	db := storagetest.Open(t)
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

// A music track is one file the player seeks in, so the stream is the whole
// file whatever position was saved last. Slicing at size*progress/duration cut
// the fLaC header off a FLAC (the browser cannot decode what is left), and
// because clients PATCH progress every 20s while they play, each rebuffer or
// seek re-opened the URL against a different slice than the one the player
// started on.
func TestStreamMusicTrackIgnoresSavedProgress(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	libraryDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("fLaC"), bytes.Repeat([]byte{0xAB}, 296)...)
	audioPath := filepath.Join(libraryDir, "03 Home with No Delay.flac")
	if err := os.WriteFile(audioPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	db := storagetest.Open(t)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path)
		VALUES ('library-1', 'Music', 'music', '', ?)`, libraryDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, duration_seconds, playback_json, added_at, updated_at)
		VALUES ('track-1', 'Home with No Delay', 300, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, mime_type, size_bytes, duration_seconds)
		VALUES ('file-1', 'library-1', 'track-1', ?, ?, ?, 'audio/flac', ?, 300)`,
		audioPath, filepath.Base(audioPath), filepath.Base(audioPath), len(payload)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO user_playback (user_id, target_kind, target_id, state_json, updated_at)
		VALUES (?, 'music-track', 'track-1', ?, CURRENT_TIMESTAMP)`,
		users.BootstrapUserID, `{"progressSeconds":120}`); err != nil {
		t.Fatal(err)
	}

	seed, err := catalogstore.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{
		Catalog:  catalog.NewService(seed),
		Playback: playback.New(db),
		Files:    files.New(db),
	})

	// "?at=120" is an explicit position, and a FLAC still cannot be cut there.
	for _, tc := range []struct{ query, rangeHeader string }{
		{"", ""}, {"", "bytes=0-"}, {"", "bytes=100-"}, {"?at=120", ""},
	} {
		rangeHeader := tc.rangeHeader
		req := httptest.NewRequest(http.MethodGet, "/api/v1/music/tracks/track-1/stream"+tc.query, nil)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK && rec.Code != http.StatusPartialContent {
			t.Fatalf("range %q: status = %d, body = %s", rangeHeader, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Samo-Stream-Offset-Seconds"); got != "" {
			t.Fatalf("range %q: offset header = %q, want the whole file", rangeHeader, got)
		}
		want := payload
		if rangeHeader == "bytes=100-" {
			want = payload[100:]
		}
		if !bytes.Equal(rec.Body.Bytes(), want) {
			t.Fatalf("range %q: body starts %q (%d bytes), want %q (%d bytes)",
				rangeHeader, rec.Body.Bytes()[:4], rec.Body.Len(), want[:4], len(want))
		}
	}
}
