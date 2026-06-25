package catalog

import (
	"testing"
	"time"
)

// TestListAudiobooks is the post-shelf-rename version of the old
// `TestListAudiobooksFiltersShelfItems`. We don't filter audiobooks out of
// podcasts anymore — they live in separate tables — so this just verifies
// the audiobook list returns the audiobook seed.
func TestListAudiobooks(t *testing.T) {
	service := NewService(Seed{
		Audiobooks: []AudiobookItem{{ID: "book-1", Book: &BookMetadata{Title: "Book"}}},
		Podcasts:   []PodcastItem{{ID: "podcast-1", Podcast: &PodcastMetadata{Title: "Podcast"}}},
	})

	page := service.ListAudiobooks(PageRequest{Limit: 10})
	if page.Total != 1 {
		t.Fatalf("total = %d, want 1", page.Total)
	}
	if page.Items[0].ID != "book-1" {
		t.Fatalf("item id = %q, want book-1", page.Items[0].ID)
	}
}

func TestListPodcasts(t *testing.T) {
	service := NewService(Seed{
		Audiobooks: []AudiobookItem{{ID: "book-1", Book: &BookMetadata{Title: "Book"}}},
		Podcasts:   []PodcastItem{{ID: "podcast-1", Podcast: &PodcastMetadata{Title: "Podcast"}}},
	})

	page := service.ListPodcasts(PageRequest{Limit: 10})
	if page.Total != 1 {
		t.Fatalf("total = %d, want 1", page.Total)
	}
	if page.Items[0].ID != "podcast-1" {
		t.Fatalf("item id = %q, want podcast-1", page.Items[0].ID)
	}
}

func TestPodcastEpisodesNewestFirst(t *testing.T) {
	oldDate := time.Date(2024, 5, 11, 12, 0, 0, 0, time.UTC)
	newDate := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	service := NewService(Seed{
		Podcasts: []PodcastItem{{ID: "podcast-1", Podcast: &PodcastMetadata{Title: "Podcast"}}},
		PodcastEpisodes: []PodcastEpisode{
			{ID: "old", PodcastID: "podcast-1", Title: "A old title", PublishedAt: &oldDate},
			{ID: "new", PodcastID: "podcast-1", Title: "Z new title", PublishedAt: &newDate},
		},
	})

	page, err := service.EpisodesForPodcast("podcast-1", PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].ID != "new" {
		t.Fatalf("first episode = %q, want new", page.Items[0].ID)
	}
}

func TestServiceReplaceRefreshesIndexes(t *testing.T) {
	service := NewService(Seed{MusicTracks: []MusicTrack{{ID: "old", Title: "Old"}}})
	if _, err := service.MusicTrack("old"); err != nil {
		t.Fatal(err)
	}

	service.Replace(Seed{MusicTracks: []MusicTrack{{ID: "new", Title: "New"}}})

	if _, err := service.MusicTrack("old"); err == nil {
		t.Fatal("old track should not remain indexed after replace")
	}
	track, err := service.MusicTrack("new")
	if err != nil {
		t.Fatal(err)
	}
	if track.Title != "New" {
		t.Fatalf("title = %q, want New", track.Title)
	}
}
