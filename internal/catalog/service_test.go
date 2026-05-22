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

func TestShelfSearchMatchesNarratorsAndSeries(t *testing.T) {
	service := NewService(Seed{ShelfItems: []ShelfItem{{
		ID:        "book-1",
		MediaType: ShelfMediaTypeBook,
		Book: &BookMetadata{
			Title:     "The Quiet Archive",
			Narrators: []Contributor{{Name: "Nora Noise"}},
			Series:    []SeriesRef{{Name: "Signal House"}},
		},
	}}})

	byNarrator := service.SearchShelf("nora", PageRequest{Limit: 10})
	if byNarrator.Total != 1 {
		t.Fatalf("narrator search total = %d, want 1", byNarrator.Total)
	}

	bySeries := service.SearchShelf("signal house", PageRequest{Limit: 10})
	if bySeries.Total != 1 {
		t.Fatalf("series search total = %d, want 1", bySeries.Total)
	}
}

func TestMusicSearchMatchesAlbumArtist(t *testing.T) {
	service := NewService(Seed{MusicAlbums: []MusicAlbum{{
		ID:            "album-1",
		Title:         "Night Broadcasts",
		ArtistNames:   []string{"The Static"},
		ReleaseType:   "album",
		ReleaseStatus: "official",
	}}})

	results := service.SearchMusic("static", PageRequest{Limit: 10})
	if results.Total != 1 {
		t.Fatalf("total = %d, want 1", results.Total)
	}
	if results.Albums[0].ID != "album-1" {
		t.Fatalf("album id = %q, want album-1", results.Albums[0].ID)
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
