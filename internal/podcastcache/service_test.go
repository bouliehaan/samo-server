package podcastcache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/podcaststream"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestEnsureCachedDownloadsAndLookupServesPath(t *testing.T) {
	ctx := context.Background()
	payload := []byte("cached-podcast-bytes")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
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

	service, err := New(db, Options{
		CacheDir: filepath.Join(root, "podcast-cache"),
		Enabled:  true,
		Stream:   podcaststream.New(podcaststream.ServiceOptions{AllowPrivateHosts: true}),
	})
	if err != nil {
		t.Fatal(err)
	}

	episode := catalog.PodcastEpisode{ID: "ep-1", EnclosureURL: upstream.URL, EnclosureType: "audio/mpeg"}
	if err := service.EnsureCached(ctx, episode); err != nil {
		t.Fatal(err)
	}
	cached, ok, err := service.Lookup(ctx, episode.ID, episode.EnclosureURL)
	if err != nil || !ok {
		t.Fatalf("lookup ok = %v err = %v", ok, err)
	}
	data, err := os.ReadFile(cached.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Fatalf("cache bytes = %q", data)
	}
}

func TestClearAllRemovesRowsAndFiles(t *testing.T) {
	ctx := context.Background()
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
		INSERT INTO podcast_episodes (id, library_id, podcast_id, title, enclosure_url)
		VALUES ('ep-1', 'lib-1', 'pod-1', 'Episode', 'https://example.com/ep-1.mp3')`); err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(root, "podcast-cache")
	service, err := New(db, Options{CacheDir: cacheDir, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(cacheDir, "ep-1.mp3")
	if err := os.WriteFile(cachePath, []byte("cached-audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "orphan.part"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcast_episode_cache (
		  episode_id, enclosure_url, cache_path, content_type, size_bytes, downloaded_at, last_accessed_at
		)
		VALUES ('ep-1', 'https://example.com/ep-1.mp3', ?, 'audio/mpeg', 12, ?, ?)`,
		cachePath, now, now); err != nil {
		t.Fatal(err)
	}

	result, err := service.ClearAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.EpisodesRemoved != 1 || result.BytesFreed != 12 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("cache file still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "orphan.part")); !os.IsNotExist(err) {
		t.Fatalf("orphan.part still present: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM podcast_episode_cache`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cache rows = %d", count)
	}
}

func TestPruneRetentionRemovesOldestWhenOverMaxBytes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(root, "podcast-cache")
	service, err := New(db, Options{
		CacheDir: cacheDir,
		Enabled:  true,
		MaxBytes: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path)
		VALUES ('lib', 'Podcasts', 'podcast', 'samo://podcast-feeds')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcasts (id, library_id, path)
		VALUES ('pod', 'lib', 'samo://show')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcast_episodes (id, library_id, podcast_id, title, enclosure_url)
		VALUES ('ep-old', 'lib', 'pod', 'Old', 'https://example.com/ep-old.mp3'),
		       ('ep-new', 'lib', 'pod', 'New', 'https://example.com/ep-new.mp3')`); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"ep-old", "ep-new"} {
		path := filepath.Join(cacheDir, id+".mp3")
		if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
			t.Fatal(err)
		}
		accessed := now
		if index == 0 {
			accessed = time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO podcast_episode_cache (
			  episode_id, enclosure_url, cache_path, content_type, size_bytes, downloaded_at, last_accessed_at
			)
			VALUES (?, ?, ?, 'audio/mpeg', 5, ?, ?)`,
			id, "https://example.com/"+id+".mp3", path, now, accessed); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.PruneRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "ep-old.mp3")); !os.IsNotExist(err) {
		t.Fatalf("old cache file still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "ep-new.mp3")); err != nil {
		t.Fatalf("new cache file missing: %v", err)
	}
}
