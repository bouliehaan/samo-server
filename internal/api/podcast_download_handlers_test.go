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
	"github.com/bouliehaan/samo-server/internal/users"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestCachePodcastEpisodeDownloadsEnclosure(t *testing.T) {
	ctx := context.Background()
	payload := []byte("downloaded-episode-bytes")
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

	userService := users.New(users.ServiceOptions{DB: db})
	bootstrap, err := userService.BootstrapWithResult(ctx, users.BootstrapInput{
		AdminUsername: "admin",
		AdminPassword: "password123",
	})
	if err != nil || !bootstrap.CreatedAdmin {
		t.Fatal(err)
	}
	login, err := userService.Login(ctx, users.LoginInput{Username: "admin", Password: "password123"})
	if err != nil {
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
		Users:         userService,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/podcasts/episodes/ep-1/cache", nil)
	req.Header.Set("Authorization", "Bearer "+login.Token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if cached, ok, err := cacheService.Lookup(ctx, "ep-1", upstream.URL); err != nil || !ok || cached.SizeBytes != int64(len(payload)) {
		t.Fatalf("lookup cached=%v ok=%v err=%v size=%d", cached, ok, err, cached.SizeBytes)
	}
}
