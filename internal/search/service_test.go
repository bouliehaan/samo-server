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

func TestSearchAudiobooksMatchesNarratorsAndSeries(t *testing.T) {
	service := New()
	service.Rebuild(catalog.Seed{Audiobooks: []catalog.AudiobookItem{{
		ID: "book-1",
		Book: &catalog.BookMetadata{
			Title:     "The Quiet Archive",
			Narrators: []catalog.ContributorRef{{Name: "Nora Noise"}},
			Series:    []catalog.SeriesRef{{Name: "Signal House"}},
		},
	}}})

	byNarrator := service.SearchAudiobooksText("nora", catalog.PageRequest{Limit: 10})
	if byNarrator.Total != 1 {
		t.Fatalf("narrator search total = %d", byNarrator.Total)
	}

	bySeries := service.SearchAudiobooksText("signal house", catalog.PageRequest{Limit: 10})
	if bySeries.Total != 1 {
		t.Fatalf("series search total = %d", bySeries.Total)
	}
}

func TestSearchPodcastsMatchesTitleAndEpisodes(t *testing.T) {
	service := New()
	service.Rebuild(catalog.Seed{
		Podcasts: []catalog.PodcastItem{{
			ID:      "podcast-1",
			Podcast: &catalog.PodcastMetadata{Title: "Signal Daily", Author: "Nora Noise"},
		}},
		PodcastEpisodes: []catalog.PodcastEpisode{{
			ID:        "ep-1",
			PodcastID: "podcast-1",
			Title:     "Episode One",
			Subtitle:  "Pilot",
		}},
	})

	byShow := service.SearchPodcastsText("signal daily", catalog.PageRequest{Limit: 10})
	if byShow.Total != 1 || len(byShow.Podcasts) != 1 {
		t.Fatalf("show search = %#v", byShow)
	}
	byEpisode := service.SearchPodcastsText("pilot", catalog.PageRequest{Limit: 10})
	if byEpisode.Total != 1 || len(byEpisode.Episodes) != 1 {
		t.Fatalf("episode search = %#v", byEpisode)
	}
}

func TestSearchPodcastsMatchesEpisodeRSSURLs(t *testing.T) {
	spotifyURL := "https://podcasters.spotify.com/pod/show/dudegrowsshow/episodes/Flip-to-Flower-Like-THIS-for-Bigger--Frostier-Buds-e3jp81a"
	enclosureURL := "https://anchor.fm/s/bcf35298/podcast/play/120413743/https%3A%2F%2Fd3ctxlq1ktw2nl.cloudfront.net%2Fstaging%2F2026-4-23%2F424721644-44100-2-a0bde5ce88cba.mp3"
	service := New()
	service.Rebuild(catalog.Seed{
		Podcasts: []catalog.PodcastItem{{
			ID:      "podcast-1",
			Podcast: &catalog.PodcastMetadata{Title: "The Dude Grows Show"},
		}},
		PodcastEpisodes: []catalog.PodcastEpisode{{
			ID:           "ep-1",
			PodcastID:    "podcast-1",
			Title:        "Flip to Flower Like THIS for Bigger, Frostier Buds",
			EnclosureURL: enclosureURL,
			ExternalIDs: catalog.ExternalIDs{
				FeedGUID: "f844e91b-37b0-4773-a4a8-21ff900c3664",
				URLs:     []string{spotifyURL, enclosureURL},
			},
		}},
	})

	bySpotify := service.SearchPodcastsText(spotifyURL, catalog.PageRequest{Limit: 10})
	if bySpotify.Total != 1 || len(bySpotify.Episodes) != 1 {
		t.Fatalf("spotify url search = %#v", bySpotify)
	}
	byEnclosure := service.SearchPodcastsText(enclosureURL, catalog.PageRequest{Limit: 10})
	if byEnclosure.Total != 1 || len(byEnclosure.Episodes) != 1 {
		t.Fatalf("enclosure url search = %#v", byEnclosure)
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

func TestSearchMusicFiltersPrivatePlaylists(t *testing.T) {
	service := New()
	service.Rebuild(catalog.Seed{MusicPlaylists: []catalog.MusicPlaylist{
		{ID: "private-other", Name: "Christmas Secret", OwnerID: "user-other"},
		{ID: "public-other", Name: "Christmas Shared", OwnerID: "user-other", Public: true},
		{ID: "private-owned", Name: "Christmas Mine", OwnerID: "user-me"},
	}})

	results := service.SearchMusic(MusicQuery{
		Text:                  "christmas",
		PlaylistUserID:        "user-me",
		FilterPlaylistsByUser: true,
		Page:                  catalog.PageRequest{Limit: 10},
	}, PlaybackOverlay{})
	if len(results.Playlists) != 2 || results.Total != 2 {
		t.Fatalf("results = %#v", results)
	}
	for _, item := range results.Playlists {
		if item.ID == "private-other" {
			t.Fatalf("private playlist leaked: %#v", results.Playlists)
		}
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
