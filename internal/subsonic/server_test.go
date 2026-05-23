package subsonic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/search"
)

func TestPingReturnsSubsonicEnvelope(t *testing.T) {
	handler := New(Options{
		Catalog:       catalog.NewService(catalog.Seed{}),
		ServerVersion: "test",
	})

	req := httptest.NewRequest(http.MethodGet, "/rest/ping.view?f=json&v=1.16.1&c=test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload map[string]map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	response := payload["subsonic-response"]
	if response["status"] != "ok" {
		t.Fatalf("status = %v, want ok", response["status"])
	}
	if response["type"] != serverType {
		t.Fatalf("type = %v, want %s", response["type"], serverType)
	}
}

func TestAuthRequiresTokenWhenConfigured(t *testing.T) {
	handler := New(Options{
		Catalog:       catalog.NewService(catalog.Seed{}),
		APIToken:      "secret",
		ServerVersion: "test",
	})

	req := httptest.NewRequest(http.MethodGet, "/rest/ping.view?f=json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload map[string]map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["subsonic-response"]["status"] != "failed" {
		t.Fatalf("status = %v, want failed", payload["subsonic-response"]["status"])
	}

	req = httptest.NewRequest(http.MethodGet, "/rest/ping.view?f=json&p=secret", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["subsonic-response"]["status"] != "ok" {
		t.Fatalf("status = %v, want ok", payload["subsonic-response"]["status"])
	}
}

func TestGetAlbumReturnsSongs(t *testing.T) {
	handler := New(Options{
		Catalog: catalog.NewService(catalog.Seed{
			MusicAlbums: []catalog.MusicAlbum{{
				ID:          "album-1",
				Title:       "Night Broadcasts",
				ArtistIDs:   []string{"artist-1"},
				ArtistNames: []string{"The Static"},
				ReleaseYear: 2026,
			}},
			MusicTracks: []catalog.MusicTrack{{
				ID:              "track-1",
				Title:           "Signal One",
				AlbumID:         "album-1",
				AlbumTitle:      "Night Broadcasts",
				ArtistIDs:       []string{"artist-1"},
				ArtistNames:     []string{"The Static"},
				TrackNumber:     1,
				DurationSeconds: 245,
				AudioFiles: []catalog.AudioFile{{
					ID:       "file-1",
					Path:     "/music/01.flac",
					MimeType: "audio/flac",
					Bitrate:  960000,
				}},
			}},
		}),
	})

	req := httptest.NewRequest(http.MethodGet, "/rest/getAlbum.view?id=album-1&f=json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var payload map[string]map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	album := payload["subsonic-response"]["album"].(map[string]any)
	songs := album["song"].([]any)
	if len(songs) != 1 {
		t.Fatalf("song count = %d, want 1", len(songs))
	}
}

func TestSearch3ReturnsMatches(t *testing.T) {
	seed := catalog.Seed{
		MusicTracks: []catalog.MusicTrack{{
			ID:          "track-1",
			Title:       "Signal One",
			ArtistNames: []string{"The Static"},
		}},
	}
	searchService := search.New()
	searchService.Rebuild(seed)
	handler := New(Options{
		Catalog: catalog.NewService(seed),
		Search:  searchService,
	})

	req := httptest.NewRequest(http.MethodGet, "/rest/search3.view?query=signal&f=json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var payload map[string]map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	results := payload["subsonic-response"]["searchResult3"].(map[string]any)
	songs := results["song"].([]any)
	if len(songs) != 1 {
		t.Fatalf("song count = %d, want 1", len(songs))
	}
}
