package search

import (
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func TestSearchMusicMatchesAlbumArtistAndFiltersGenre(t *testing.T) {
	service := New()
	service.Rebuild(catalog.Seed{MusicAlbums: []catalog.MusicAlbum{{
		ID:          "album-1",
		Title:       "Night Broadcasts",
		ArtistNames: []string{"The Static"},
		Genres:      []string{"Electronic"},
		ReleaseYear: 2020,
	}}})

	results := service.SearchMusicText("static", catalog.PageRequest{Limit: 10})
	if results.Total != 1 || len(results.Albums) != 1 {
		t.Fatalf("results = %#v", results)
	}

	filtered := service.SearchMusic(MusicQuery{
		Genre: "electronic",
		Page:  catalog.PageRequest{Limit: 10},
	}, PlaybackOverlay{})
	if filtered.Total != 1 {
		t.Fatalf("genre filter total = %d", filtered.Total)
	}

	miss := service.SearchMusic(MusicQuery{
		Genre: "jazz",
		Page:  catalog.PageRequest{Limit: 10},
	}, PlaybackOverlay{})
	if miss.Total != 0 {
		t.Fatalf("unexpected jazz match total = %d", miss.Total)
	}
}

func TestSearchShelfMatchesNarratorsAndSeries(t *testing.T) {
	service := New()
	service.Rebuild(catalog.Seed{ShelfItems: []catalog.ShelfItem{{
		ID:        "book-1",
		MediaType: catalog.ShelfMediaTypeBook,
		Book: &catalog.BookMetadata{
			Title:     "The Quiet Archive",
			Narrators: []catalog.Contributor{{Name: "Nora Noise"}},
			Series:    []catalog.SeriesRef{{Name: "Signal House"}},
		},
	}}})

	byNarrator := service.SearchShelfText("nora", catalog.PageRequest{Limit: 10})
	if byNarrator.Total != 1 {
		t.Fatalf("narrator search total = %d", byNarrator.Total)
	}

	bySeries := service.SearchShelfText("signal house", catalog.PageRequest{Limit: 10})
	if bySeries.Total != 1 {
		t.Fatalf("series search total = %d", bySeries.Total)
	}
}

func TestSearchMusicFavoriteFilterUsesOverlay(t *testing.T) {
	service := New()
	service.Rebuild(catalog.Seed{MusicTracks: []catalog.MusicTrack{
		{ID: "track-1", Title: "Alpha"},
		{ID: "track-2", Title: "Beta"},
	}})

	favorite := true
	results := service.SearchMusic(MusicQuery{
		Favorite: &favorite,
		Page:     catalog.PageRequest{Limit: 10},
	}, PlaybackOverlay{
		Tracks: map[string]catalog.PlaybackState{
			"track-2": {Favorite: true},
		},
	})
	if results.Total != 1 || len(results.Tracks) != 1 || results.Tracks[0].ID != "track-2" {
		t.Fatalf("results = %#v", results)
	}
}

func TestSearchMusicSortsByLastPlayed(t *testing.T) {
	now := time.Now().UTC()
	earlier := now.Add(-time.Hour)
	service := New()
	service.Rebuild(catalog.Seed{MusicTracks: []catalog.MusicTrack{
		{ID: "track-1", Title: "Alpha"},
		{ID: "track-2", Title: "Beta"},
	}})

	results := service.SearchMusic(MusicQuery{
		Sort: SortPlayed,
		Page: catalog.PageRequest{Limit: 10},
	}, PlaybackOverlay{
		Tracks: map[string]catalog.PlaybackState{
			"track-1": {LastPlayedAt: &earlier},
			"track-2": {LastPlayedAt: &now},
		},
	})
	if len(results.Tracks) != 2 || results.Tracks[0].ID != "track-2" {
		t.Fatalf("tracks = %#v", results.Tracks)
	}
}

func TestTokenizeRequiresAllTerms(t *testing.T) {
	if !MatchText("the quiet archive nora noise", "quiet nora") {
		t.Fatal("expected multi-token match")
	}
	if MatchText("the quiet archive", "quiet missing") {
		t.Fatal("expected miss on missing token")
	}
}
