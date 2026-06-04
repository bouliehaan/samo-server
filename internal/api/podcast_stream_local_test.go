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
	"github.com/bouliehaan/samo-server/migrations"
)

func TestStreamPodcastEpisodeServesLocalFileFromStart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	podcastDir := filepath.Join(root, "podcasts", "Daily Show")
	if err := os.MkdirAll(podcastDir, 0o755); err != nil {
		t.Fatal(err)
	}

	audioPath := filepath.Join(podcastDir, "episode-1.mp3")
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

	libraryID := "lib-pod"
	podcastID := "pod-1"
	episodeID := "ep-1"
	fileID := "file-1"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path)
		VALUES (?, 'Podcasts', 'podcast', ?)`,
		libraryID, filepath.Join(root, "podcasts")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcasts (id, library_id, path, duration_seconds)
		VALUES (?, ?, '/podcasts/daily', 0)`, podcastID, libraryID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcast_episodes (
		  id, library_id, podcast_id, title, duration_seconds, progress_json
		)
		VALUES (?, ?, ?, 'Episode 1', 10, '{}')`,
		episodeID, libraryID, podcastID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (
		  id, library_id, podcast_id, episode_id, path, relative_path, file_name,
		  mime_type, size_bytes, duration_seconds
		)
		VALUES (?, ?, ?, ?, ?, 'episode-1.mp3', 'episode-1.mp3', 'audio/mpeg', ?, 10)`,
		fileID, libraryID, podcastID, episodeID, audioPath, len(payload)); err != nil {
		t.Fatal(err)
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{
		Catalog:  catalog.NewService(seed),
		Playback: playback.New(db),
		Files:    files.New(db),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/podcasts/episodes/"+episodeID+"/stream?offsetSeconds=0", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK && rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Samo-Media-File-Id"); got != fileID {
		t.Fatalf("media file header = %q, want %s", got, fileID)
	}
	if !bytes.Equal(rec.Body.Bytes(), payload) {
		t.Fatalf("body = %q, want full file bytes", rec.Body.Bytes())
	}
}
