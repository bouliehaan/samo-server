package sources

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestSavePodcastFeedPreservesOverriddenTitleInDatabase(t *testing.T) {
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
	if err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "Original Show"}); err != nil {
		t.Fatal(err)
	}
	feedID := podcastFeedID(feedURL)

	if err := catalog.UpsertMetadataOverride(ctx, db, catalog.OverrideKindPodcastFeed, feedID, catalog.MetadataOverridePatch{
		"title": []byte(`"User Show Title"`),
	}); err != nil {
		t.Fatal(err)
	}

	if err := service.savePodcastFeed(ctx, feedURL, parsedPodcastFeed{Title: "RSS Replacement"}); err != nil {
		t.Fatal(err)
	}

	var storedTitle string
	if err := db.QueryRowContext(ctx, `SELECT title FROM podcast_feeds WHERE id = ?`, feedID).Scan(&storedTitle); err != nil {
		t.Fatal(err)
	}
	if storedTitle != "Original Show" {
		t.Fatalf("stored title = %q, want preserved original source title", storedTitle)
	}

	feed, err := service.GetPodcastFeed(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}
	if feed.Title != "User Show Title" {
		t.Fatalf("projected feed title = %q", feed.Title)
	}
}
