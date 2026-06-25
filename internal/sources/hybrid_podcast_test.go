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

func TestHybridPodcastFeedMergesLocalEpisode(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	libraryID := "library_podcasts"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path, description)
		VALUES (?, 'Podcasts', 'podcast', ?, 'test library')`,
		libraryID, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	podcastID := stableID("podcast", libraryID, "Hardcore History")
	episodeID := stableID("episode", podcastID, "show/episode-one.mp3")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcasts (id, library_id, path, folder_id, podcast_json)
		VALUES (?, ?, 'Hardcore History', 'folder', '{"title":"Hardcore History"}')`,
		podcastID, libraryID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcast_episodes (id, library_id, podcast_id, title, published_at)
		VALUES (?, ?, ?, 'Episode One', '2010-01-01T00:00:00Z')`,
		episodeID, libraryID, podcastID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, podcast_id, episode_id, path, relative_path, file_name)
		VALUES ('file-1', ?, ?, ?, ?, 'Hardcore History/episode-one.mp3', 'episode-one.mp3')`,
		libraryID, podcastID, episodeID, t.TempDir()+"/Hardcore History/episode-one.mp3"); err != nil {
		t.Fatal(err)
	}

	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Hardcore History</title>
    <item>
      <title>Episode One</title>
      <guid>episode-1</guid>
      <pubDate>Wed, 01 Sep 2010 12:00:00 GMT</pubDate>
      <enclosure url="https://cdn.example.com/ep1.mp3" type="audio/mpeg" length="1234" />
    </item>
    <item>
      <title>Episode Two</title>
      <guid>episode-2</guid>
      <pubDate>Wed, 01 Oct 2025 12:00:00 GMT</pubDate>
      <enclosure url="https://cdn.example.com/ep2.mp3" type="audio/mpeg" length="1234" />
    </item>
  </channel>
</rss>`))
	}))
	defer feedServer.Close()

	service := New(db)
	if _, err := service.AddPodcastFeed(ctx, AddPodcastFeedInput{
		URL:       feedServer.URL + "/feed.xml",
		PodcastID: podcastID,
	}); err != nil {
		t.Fatal(err)
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed.Podcasts) != 1 {
		t.Fatalf("podcasts = %d, want 1", len(seed.Podcasts))
	}
	if seed.Podcasts[0].Podcast == nil || seed.Podcasts[0].Podcast.FeedURL == "" {
		t.Fatalf("expected hybrid podcast feed url on show metadata")
	}
	if len(seed.PodcastEpisodes) != 2 {
		t.Fatalf("episodes = %d, want 2 (merged local + new rss)", len(seed.PodcastEpisodes))
	}

	var merged *catalog.PodcastEpisode
	for index := range seed.PodcastEpisodes {
		episode := seed.PodcastEpisodes[index]
		if episode.ID == episodeID {
			merged = &episode
			break
		}
	}
	if merged == nil {
		t.Fatal("local episode row was not preserved")
	}
	if merged.EnclosureURL != "https://cdn.example.com/ep1.mp3" {
		t.Fatalf("enclosure = %q, want rss enclosure on merged episode", merged.EnclosureURL)
	}
	if len(merged.AudioFiles) == 0 {
		t.Fatal("merged episode should still reference local media file")
	}
}

func TestFindMatchingEpisodeByGUID(t *testing.T) {
	existing := []existingPodcastEpisode{{
		ID:    "episode-local",
		Title: "Show 42",
		ExternalIDs: catalog.ExternalIDs{
			FeedGUID: "abc-123",
		},
	}}
	parsed := parsedPodcastEpisode{
		Title: "Different Title",
		GUID:  "abc-123",
	}
	match := findMatchingEpisode(parsed, existing, map[string]struct{}{})
	if match == nil || match.ID != "episode-local" {
		t.Fatalf("match = %#v, want episode-local", match)
	}
}
