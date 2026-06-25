package catalog

import (
	"testing"
	"time"
)

func TestListRecentlyAddedMergesMediaKinds(t *testing.T) {
	oldest := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	middle := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	newest := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{
			{ID: "album-1", Title: "Album", DisplayArtist: "Artist", AddedAt: &middle},
		},
		Audiobooks: []AudiobookItem{
			{
				ID:      "book-1",
				AddedAt: &newest,
				Book:    &BookMetadata{Title: "Book", Authors: []ContributorRef{{Name: "Author"}}},
			},
		},
		Podcasts: []PodcastItem{
			{
				ID:      "show-1",
				AddedAt: &oldest,
				Podcast: &PodcastMetadata{Title: "Show", Author: "Host"},
			},
		},
	})

	results := service.ListRecentlyAdded(PageRequest{Limit: 10})
	if len(results.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(results.Items))
	}
	if results.Items[0].Kind != RecentlyAddedKindAudiobook || results.Items[0].ID != "book-1" {
		t.Fatalf("first = %#v, want audiobook book-1", results.Items[0])
	}
	if results.Items[1].Kind != RecentlyAddedKindMusicAlbum {
		t.Fatalf("second = %#v, want music album", results.Items[1])
	}
	if results.Items[2].Kind != RecentlyAddedKindPodcast {
		t.Fatalf("third = %#v, want podcast", results.Items[2])
	}
}

func TestListRecentlyAddedSkipsMissingItems(t *testing.T) {
	service := NewService(Seed{
		Audiobooks: []AudiobookItem{
			{ID: "missing", Missing: true, Book: &BookMetadata{Title: "Gone"}},
		},
	})

	results := service.ListRecentlyAdded(PageRequest{Limit: 10})
	if len(results.Items) != 0 {
		t.Fatalf("items = %#v, want none", results.Items)
	}
}
