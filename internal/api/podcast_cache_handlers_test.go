package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/podcastcache"
	"github.com/bouliehaan/samo-server/internal/podcaststream"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestStreamPodcastEpisodeServesCachedEnclosureBytes(t *testing.T) {
	ctx := context.Background()
	payload := []byte("cached-stream-bytes-12345")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		VALUES ('lib-1', 'Podcasts', 'podcast', 'samo://podcast-feeds')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcasts (id, library_id, path)
		VALUES ('pod-1', 'lib-1', 'samo://show')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcast_episodes (
		  id, library_id, podcast_id, title, duration_seconds, enclosure_url, enclosure_type, enclosure_bytes
		)
		VALUES ('ep-1', 'lib-1', 'pod-1', 'Episode', 10, ?, 'audio/mpeg', ?)`,
		upstream.URL, len(payload)); err != nil {
		t.Fatal(err)
	}

	cacheService, err := podcastcache.New(db, podcastcache.Options{
		CacheDir: filepath.Join(root, "podcast-cache"),
		Enabled:  true,
		Stream:   podcaststream.New(podcaststream.ServiceOptions{AllowPrivateHosts: true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cacheService.EnsureCached(ctx, catalog.PodcastEpisode{
		ID: "ep-1", EnclosureURL: upstream.URL, EnclosureType: "audio/mpeg",
	}); err != nil {
		t.Fatal(err)
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{
		Catalog:       catalog.NewService(seed),
		Files:         files.New(db, cacheService.CacheDir()),
		PodcastCache:  cacheService,
		PodcastStream: podcaststream.New(podcaststream.ServiceOptions{AllowPrivateHosts: true}),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/podcasts/episodes/ep-1/stream", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Samo-Stream-Source"); got != "cache" {
		t.Fatalf("source = %q, want cache", got)
	}
	if rec.Body.String() != string(payload) {
		t.Fatalf("body = %q", rec.Body.Bytes())
	}
}
