package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/podcastcache"
	"github.com/bouliehaan/samo-server/internal/podcaststream"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestAddPodcastFeedAutoDownloadPrefetchesEpisodes(t *testing.T) {
	ctx := context.Background()
	payload := []byte("episode-bytes-for-cache")
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

	cacheService, err := podcastcache.New(db, podcastcache.Options{
		CacheDir: filepath.Join(root, "podcast-cache"),
		Enabled:  true,
		Stream:   podcaststream.New(podcaststream.ServiceOptions{AllowPrivateHosts: true}),
	})
	if err != nil {
		t.Fatal(err)
	}

	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Auto Download Show</title>
    <item>
      <title>Episode One</title>
      <guid>episode-1</guid>
      <enclosure url="` + upstream.URL + `" type="audio/mpeg" length="1234" />
    </item>
  </channel>
</rss>`))
	}))
	defer feedServer.Close()

	service := New(db, Options{PodcastCache: cacheService})
	enabled := true
	feed, err := service.AddPodcastFeed(ctx, AddPodcastFeedInput{
		URL:                 feedServer.URL + "/feed.xml",
		AutoDownloadEnabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !feed.AutoDownloadEnabled {
		t.Fatal("expected auto download enabled on new feed")
	}

	deadline := time.Now().Add(5 * time.Second)
	var episodeID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM podcast_episodes WHERE podcast_id = ?`, feed.PodcastID).Scan(&episodeID); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		if cached, ok, err := cacheService.Lookup(ctx, episodeID, upstream.URL); err == nil && ok && cached.SizeBytes > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, ok, err := cacheService.Lookup(ctx, episodeID, upstream.URL); err != nil || !ok {
		t.Fatalf("expected cached episode, ok=%v err=%v", ok, err)
	}
}

func TestUpdatePodcastFeedAutoDownloadEnabled(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	feedURL := "https://example.com/feed.xml"
	if err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Test Show"}); err != nil {
		t.Fatal(err)
	}
	feedID := podcastFeedID(feedURL)

	enabled := true
	updated, err := service.UpdatePodcastFeed(ctx, feedID, UpdatePodcastFeedInput{
		AutoDownloadEnabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.AutoDownloadEnabled {
		t.Fatal("expected auto download enabled")
	}

	disabled := false
	updated, err = service.UpdatePodcastFeed(ctx, feedID, UpdatePodcastFeedInput{
		AutoDownloadEnabled: &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AutoDownloadEnabled {
		t.Fatal("expected auto download disabled")
	}
}

func TestSavePodcastFeedPreservesAutoDownloadOnRefresh(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	feedURL := "https://example.com/feed.xml"
	if err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Test Show"}, feedSaveOptions{autoDownloadOnInsert: true}); err != nil {
		t.Fatal(err)
	}
	feedID := podcastFeedID(feedURL)

	if err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Renamed Show"}); err != nil {
		t.Fatal(err)
	}
	feed, err := service.GetPodcastFeed(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}
	if !feed.AutoDownloadEnabled {
		t.Fatal("expected auto download preserved after refresh")
	}
}
