package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestAddPodcastFeedCreatesPodcastAndEpisodes(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
  <channel>
    <title>Night Signals</title>
    <description>Late radio stories</description>
    <itunes:author>Ada Archive</itunes:author>
    <item>
      <title>Episode One</title>
      <link>https://example.com/episodes/one</link>
      <guid>episode-1</guid>
      <itunes:duration>600</itunes:duration>
      <enclosure url="https://cdn.example.com/ep1.mp3" type="audio/mpeg" length="1234" />
    </item>
  </channel>
</rss>`))
	}))
	defer feedServer.Close()

	service := New(db)
	feed, err := service.AddPodcastFeed(ctx, AddPodcastFeedInput{URL: feedServer.URL + "/feed.xml"})
	if err != nil {
		t.Fatal(err)
	}
	if feed.Title != "Night Signals" {
		t.Fatalf("feed title = %q, want Night Signals", feed.Title)
	}
	if feed.EpisodeCount != 1 {
		t.Fatalf("episode count = %d, want 1", feed.EpisodeCount)
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed.Podcasts) != 1 {
		t.Fatalf("podcasts = %d, want 1", len(seed.Podcasts))
	}
	if seed.Podcasts[0].Podcast == nil || seed.Podcasts[0].Podcast.FeedURL == "" {
		t.Fatalf("podcast metadata = %#v, want feed metadata", seed.Podcasts[0].Podcast)
	}
	if len(seed.PodcastEpisodes) != 1 {
		t.Fatalf("episodes = %d, want 1", len(seed.PodcastEpisodes))
	}
	if seed.PodcastEpisodes[0].EnclosureURL != "https://cdn.example.com/ep1.mp3" {
		t.Fatalf("enclosure url = %q", seed.PodcastEpisodes[0].EnclosureURL)
	}
	if !stringSliceContains(seed.PodcastEpisodes[0].ExternalIDs.URLs, "https://example.com/episodes/one") {
		t.Fatalf("episode external urls = %#v, want RSS item link", seed.PodcastEpisodes[0].ExternalIDs.URLs)
	}
}

func TestAddPodcastFeedDiscoversRSSAlternateFromShowPage(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/show":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><link rel="alternate" type="application/rss+xml" title="Night Signals" href="/feed.xml"></head><body></body></html>`))
		case "/feed.xml":
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Night Signals</title>
    <item>
      <title>Episode One</title>
      <guid>episode-1</guid>
      <enclosure url="https://cdn.example.com/ep1.mp3" type="audio/mpeg" length="1234" />
    </item>
  </channel>
</rss>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer feedServer.Close()

	service := New(db)
	feed, err := service.AddPodcastFeed(ctx, AddPodcastFeedInput{URL: feedServer.URL + "/show"})
	if err != nil {
		t.Fatal(err)
	}
	if feed.FeedURL != feedServer.URL+"/feed.xml" {
		t.Fatalf("feed url = %q, want discovered RSS URL", feed.FeedURL)
	}
	if feed.EpisodeCount != 1 {
		t.Fatalf("episode count = %d, want 1", feed.EpisodeCount)
	}
}

func TestAddInternetRadioStationIsIdempotentByStreamURL(t *testing.T) {
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
	first, err := service.AddInternetRadioStation(ctx, AddInternetRadioStationInput{
		Name:      "Static FM",
		StreamURL: "https://radio.example.com/live.mp3",
		Tags:      []string{"old time radio", "drama"},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.AddInternetRadioStation(ctx, AddInternetRadioStationInput{
		Name:      "Static FM Updated",
		StreamURL: "https://radio.example.com/live.mp3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("ids = %q and %q, want same id", first.ID, second.ID)
	}
	if second.Name != "Static FM Updated" {
		t.Fatalf("name = %q, want updated name", second.Name)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
