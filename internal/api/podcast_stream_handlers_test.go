package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/podcaststream"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/users"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestStreamPodcastEpisodeProxiesRemoteEnclosure(t *testing.T) {
	ctx := context.Background()
	payload := []byte("0123456789abcdefghij")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=10-" {
			w.Header().Set("Content-Range", "bytes 10-19/20")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[10:])
			return
		}
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path)
		VALUES ('lib-pod', 'Podcasts', 'podcast', ?)`, filepath.Join(root, "podcasts")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcasts (id, library_id, path, duration_seconds)
		VALUES ('pod-1', 'lib-pod', '/remote/show', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcast_episodes (
		  id, library_id, podcast_id, title, duration_seconds, enclosure_url, enclosure_type, enclosure_bytes, progress_json
		)
		VALUES ('ep-1', 'lib-pod', 'pod-1', 'Episode', 10, ?, 'audio/mpeg', ?, '{}')`,
		upstream.URL, len(payload)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO user_playback (user_id, target_kind, target_id, state_json, updated_at)
		VALUES (?, 'podcast-episode', 'ep-1', ?, datetime('now'))`,
		users.BootstrapUserID, `{"progressSeconds":5}`); err != nil {
		t.Fatal(err)
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{
		Catalog:       catalog.NewService(seed),
		Playback:      playback.New(db),
		PodcastStream: podcaststream.New(podcaststream.ServiceOptions{AllowPrivateHosts: true}),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/podcasts/episodes/ep-1/stream", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK && rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Samo-Stream-Source"); got != "enclosure" {
		t.Fatalf("source = %q", got)
	}
	if rec.Body.String() != string(payload[10:]) {
		t.Fatalf("body = %q", rec.Body.Bytes())
	}
}
