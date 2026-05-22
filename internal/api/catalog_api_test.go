package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jakedebus/samo-server/internal/catalog"
	"github.com/jakedebus/samo-server/internal/media"
	"github.com/jakedebus/samo-server/internal/radio"
)

func TestMusicHandlersExposeRichTrackMetadata(t *testing.T) {
	handler := catalogTestServer(t, catalog.Seed{
		MusicArtists: []catalog.MusicArtist{{
			ID:          "artist-1",
			Name:        "The Static",
			Genres:      []string{"ambient"},
			ExternalIDs: catalog.ExternalIDs{MusicBrainzArtistID: "mba-1"},
		}},
		MusicAlbums: []catalog.MusicAlbum{{
			ID:               "album-1",
			Title:            "Night Broadcasts",
			AlbumArtistIDs:   []string{"artist-1"},
			AlbumArtistNames: []string{"The Static"},
			ReleaseYear:      2026,
			ReleaseType:      "album",
			Genres:           []string{"ambient"},
			ExternalIDs:      catalog.ExternalIDs{MusicBrainzReleaseID: "mbr-1"},
		}},
		MusicTracks: []catalog.MusicTrack{{
			ID:               "track-1",
			Title:            "Signal One",
			ArtistIDs:        []string{"artist-1"},
			ArtistNames:      []string{"The Static"},
			AlbumID:          "album-1",
			AlbumTitle:       "Night Broadcasts",
			AlbumArtistIDs:   []string{"artist-1"},
			AlbumArtistNames: []string{"The Static"},
			DiscNumber:       1,
			TrackNumber:      1,
			DurationSeconds:  245,
			Genres:           []string{"ambient"},
			AudioFiles: []catalog.AudioFile{{
				ID:              "file-1",
				Path:            "/music/The Static/Night Broadcasts/01 Signal One.flac",
				Container:       "flac",
				MimeType:        "audio/flac",
				Codec:           "flac",
				BitDepth:        24,
				SampleRate:      96000,
				Channels:        2,
				DurationSeconds: 245,
			}},
			ExternalIDs: catalog.ExternalIDs{ISRC: "US-SAM-26-00001"},
		}},
		Genres: []catalog.GenreSummary{{Name: "ambient", Kind: media.KindMusic, TrackCount: 1, AlbumCount: 1}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/music/tracks/track-1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var track catalog.MusicTrack
	if err := json.NewDecoder(rec.Body).Decode(&track); err != nil {
		t.Fatal(err)
	}
	if track.ExternalIDs.ISRC != "US-SAM-26-00001" {
		t.Fatalf("ISRC = %q, want seeded ISRC", track.ExternalIDs.ISRC)
	}
	if got := track.AudioFiles[0].SampleRate; got != 96000 {
		t.Fatalf("sample rate = %d, want 96000", got)
	}
}

func TestShelfHandlersExposeRichAudiobookMetadata(t *testing.T) {
	handler := catalogTestServer(t, catalog.Seed{
		ShelfLibraries: []catalog.ShelfLibrary{{
			ID:        "library-1",
			Name:      "Audiobooks",
			MediaType: catalog.ShelfMediaTypeBook,
			ItemCount: 1,
		}},
		ShelfAuthors: []catalog.ShelfAuthor{{
			ID:        "author-1",
			Name:      "Ada Archive",
			ItemCount: 1,
		}},
		ShelfSeries: []catalog.ShelfSeries{{
			ID:        "series-1",
			Name:      "Signals",
			ItemIDs:   []string{"book-1"},
			ItemCount: 1,
		}},
		ShelfItems: []catalog.ShelfItem{{
			ID:              "book-1",
			LibraryID:       "library-1",
			MediaType:       catalog.ShelfMediaTypeBook,
			Path:            "/audiobooks/Ada Archive/Signal Manual",
			DurationSeconds: 7200,
			Book: &catalog.BookMetadata{
				Title:           "Signal Manual",
				Authors:         []catalog.Contributor{{ID: "author-1", Name: "Ada Archive", Role: "author"}},
				Narrators:       []catalog.Contributor{{Name: "Nora Noise", Role: "narrator"}},
				Series:          []catalog.SeriesRef{{ID: "series-1", Name: "Signals", Sequence: 1}},
				Publisher:       "Samo Press",
				PublishedYear:   "2026",
				ISBNs:           []string{"9780000000001"},
				DurationSeconds: 7200,
				ExternalIDs:     catalog.ExternalIDs{AudibleASIN: "B000SAMO"},
			},
			AudioFiles: []catalog.AudioFile{{
				ID:              "audio-1",
				Path:            "/audiobooks/Ada Archive/Signal Manual/part-1.mp3",
				MimeType:        "audio/mpeg",
				Codec:           "mp3",
				Bitrate:         128000,
				DurationSeconds: 3600,
			}},
			Chapters: []catalog.AudioChapter{{Index: 1, Title: "Opening", StartSeconds: 0, EndSeconds: 600}},
		}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/shelf/items/book-1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var item catalog.ShelfItem
	if err := json.NewDecoder(rec.Body).Decode(&item); err != nil {
		t.Fatal(err)
	}
	if item.Book == nil {
		t.Fatal("book metadata is nil")
	}
	if item.Book.ExternalIDs.AudibleASIN != "B000SAMO" {
		t.Fatalf("Audible ASIN = %q, want seeded ASIN", item.Book.ExternalIDs.AudibleASIN)
	}
	if got := item.Chapters[0].Title; got != "Opening" {
		t.Fatalf("chapter title = %q, want Opening", got)
	}
}

func TestCatalogPaginationValidation(t *testing.T) {
	handler := catalogTestServer(t, catalog.Seed{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/music/tracks?limit=nope", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func catalogTestServer(t *testing.T, seed catalog.Seed) http.Handler {
	t.Helper()

	radioService, err := radio.NewService(radio.Config{})
	if err != nil {
		t.Fatal(err)
	}

	return NewServer(ServerOptions{
		Catalog: catalog.NewService(seed),
		Radio:   radioService,
	})
}
