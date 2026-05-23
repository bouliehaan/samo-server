package metadata

import (
	"context"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestMetadataApplyPreviewMergesBookTitle(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	itemID := "item-1"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path)
		VALUES ('lib-1', 'Books', 'shelf', 'book', '/books')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO shelf_items (id, library_id, media_type, media_kind, path, book_json)
		VALUES (?, 'lib-1', 'book', 'audiobook', '/books/old', '{"title":"Old Title"}')`, itemID); err != nil {
		t.Fatal(err)
	}

	service := NewMetadataApplyService(db)
	preview, err := service.Preview(ctx, MetadataApplyRequest{
		TargetKind: string(ApplyTargetShelfItem),
		TargetID:   itemID,
		Fields:     []string{"title", "description"},
		Candidate: SearchResult{
			MediaType:   "audiobook",
			Title:       "Signal Manual",
			Description: "A dense field guide",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.AppliedFields) != 2 {
		t.Fatalf("applied = %#v", preview.AppliedFields)
	}
	after := preview.After.(catalog.ShelfItem)
	if after.Book == nil || after.Book.Title != "Signal Manual" {
		t.Fatalf("after book = %#v", after.Book)
	}
}

func TestMetadataApplyWritesMusicArtistName(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	artistID := "artist-1"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_artists (id, name)
		VALUES (?, 'Old Name')`, artistID); err != nil {
		t.Fatal(err)
	}

	service := NewMetadataApplyService(db)
	result, err := service.Apply(ctx, MetadataApplyRequest{
		TargetKind: string(ApplyTargetMusicArtist),
		TargetID:   artistID,
		Fields:     []string{"name", "sortName"},
		Candidate: SearchResult{
			MediaType: "musicArtist",
			Title:     "The Static",
			SortTitle: "Static, The",
			ExternalIDs: catalog.ExternalIDs{
				MusicBrainzArtistID: "mbid-1",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AppliedFields) != 2 {
		t.Fatalf("applied = %#v", result.AppliedFields)
	}

	var fieldsJSON string
	if err := db.QueryRowContext(ctx, `
		SELECT fields_json FROM metadata_overrides
		WHERE target_kind = ? AND target_id = ?`, ApplyTargetMusicArtist, artistID).
		Scan(&fieldsJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fieldsJSON, `"The Static"`) || !strings.Contains(fieldsJSON, `"Static, The"`) {
		t.Fatalf("override json = %s", fieldsJSON)
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE music_artists SET name = 'Rescan Name', sort_name = '' WHERE id = ?`, artistID); err != nil {
		t.Fatal(err)
	}
	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed.MusicArtists) != 1 {
		t.Fatalf("artists = %#v", seed.MusicArtists)
	}
	if seed.MusicArtists[0].Name != "The Static" || seed.MusicArtists[0].SortName != "Static, The" {
		t.Fatalf("projected artist = %#v", seed.MusicArtists[0])
	}
}

func TestMetadataApplyPodcastFeedUpdatesCatalogProjection(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path)
		VALUES ('remote-podcast', 'Podcast Feeds', 'shelf', 'podcast', 'samo://podcasts/rss')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO shelf_items (id, library_id, media_type, media_kind, path, cover_json, genres_json, podcast_json)
		VALUES (
		  'podcast-1', 'remote-podcast', 'podcast', 'podcast', 'https://feeds.example.com/old.xml',
		  '{"url":"https://img.example.com/old.jpg"}',
		  '["old"]',
		  '{"title":"Old Show","feedUrl":"https://feeds.example.com/old.xml","categories":["old"],"externalIds":{"feedGuid":"feed-1"}}'
		)`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcast_feeds (
		  id, podcast_id, feed_url, title, description, author, site_url, image_url, language, explicit,
		  categories_json, owner_name, owner_email, episode_count
		)
		VALUES (
		  'feed-1', 'podcast-1', 'https://feeds.example.com/old.xml', 'Old Show', '', '', '',
		  'https://img.example.com/old.jpg', 'en', 0, '["old"]', 'Owner', 'owner@example.com', 12
		)`,
	); err != nil {
		t.Fatal(err)
	}

	service := NewMetadataApplyService(db)
	result, err := service.Apply(ctx, MetadataApplyRequest{
		TargetKind: string(ApplyTargetPodcastFeed),
		TargetID:   "feed-1",
		Fields:     []string{"title", "siteUrl", "imageUrl", "categories", "externalIds"},
		Candidate: SearchResult{
			MediaType: "podcast",
			Title:     "New Show",
			Genres:    []string{"fiction", "night radio"},
			Cover:     &catalog.Image{URL: "https://img.example.com/new.jpg"},
			Links:     []Link{{Label: "site", URL: "https://show.example.com"}},
			ExternalIDs: catalog.ExternalIDs{
				ITunesID: "42",
				URLs:     []string{"https://podcasts.apple.com/us/podcast/id42"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AppliedFields) != 5 {
		t.Fatalf("applied = %#v", result.AppliedFields)
	}

	var overrideCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM metadata_overrides
		WHERE target_kind = ? AND target_id = 'feed-1'`, ApplyTargetPodcastFeed).Scan(&overrideCount); err != nil {
		t.Fatal(err)
	}
	if overrideCount != 1 {
		t.Fatalf("override rows = %d", overrideCount)
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE podcast_feeds
		SET title = 'RSS Title', site_url = '', image_url = '', categories_json = '["rss"]'
		WHERE id = 'feed-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE shelf_items
		SET podcast_json = '{"title":"RSS Title","feedUrl":"https://feeds.example.com/old.xml","categories":["rss"],"externalIds":{"feedGuid":"feed-1"}}',
		    genres_json = '["rss"]'
		WHERE id = 'podcast-1'`); err != nil {
		t.Fatal(err)
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed.ShelfItems) != 1 || seed.ShelfItems[0].Podcast == nil {
		t.Fatalf("seed items = %#v", seed.ShelfItems)
	}
	item := seed.ShelfItems[0]
	if item.Podcast.Title != "New Show" || item.Podcast.SiteURL != "https://show.example.com" {
		t.Fatalf("podcast projection = %#v", item.Podcast)
	}
	if item.Podcast.ExternalIDs.FeedGUID != "feed-1" || item.Podcast.ExternalIDs.ITunesID != "42" {
		t.Fatalf("external ids = %#v", item.Podcast.ExternalIDs)
	}
	if item.Cover == nil || item.Cover.URL != "https://img.example.com/new.jpg" {
		t.Fatalf("cover = %#v", item.Cover)
	}
	if len(item.Genres) != 2 || item.Genres[0] != "fiction" {
		t.Fatalf("genres = %#v", item.Genres)
	}
}
