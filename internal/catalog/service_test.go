package catalog

import "testing"

func TestListAudiobooksFiltersShelfItems(t *testing.T) {
	service := NewService(Seed{ShelfItems: []ShelfItem{
		{ID: "book-1", MediaType: ShelfMediaTypeBook, Book: &BookMetadata{Title: "Book"}},
		{ID: "podcast-1", MediaType: ShelfMediaTypePodcast, Podcast: &PodcastMetadata{Title: "Podcast"}},
	}})

	page := service.ListAudiobooks(PageRequest{Limit: 10})
	if page.Total != 1 {
		t.Fatalf("total = %d, want 1", page.Total)
	}
	if page.Items[0].ID != "book-1" {
		t.Fatalf("item id = %q, want book-1", page.Items[0].ID)
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
