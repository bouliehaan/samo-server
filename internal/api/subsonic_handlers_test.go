package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/radio"
)

func TestSubsonicRoutesAreRegistered(t *testing.T) {
	handler := catalogTestServer(t, catalog.Seed{
		MusicArtists: []catalog.MusicArtist{{ID: "artist-1", Name: "The Static", AlbumCount: 1}},
		MusicAlbums: []catalog.MusicAlbum{{
			ID:          "album-1",
			Title:       "Night Broadcasts",
			ArtistIDs:   []string{"artist-1"},
			ArtistNames: []string{"The Static"},
		}},
	})

	req := httptest.NewRequest(http.MethodGet, "/rest/getArtists.view?f=json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload map[string]map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["subsonic-response"]["status"] != "ok" {
		t.Fatalf("status = %v, want ok", payload["subsonic-response"]["status"])
	}
}

func TestSubsonicRoutesRespectAPIToken(t *testing.T) {
	radioService, err := radio.NewService(radio.Config{})
	if err != nil {
		t.Fatal(err)
	}

	handler := NewServer(ServerOptions{
		APIToken: "secret",
		Catalog:  catalog.NewService(catalog.Seed{}),
		Radio:    radioService,
	})

	req := httptest.NewRequest(http.MethodGet, "/rest/ping.view?f=json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var payload map[string]map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["subsonic-response"]["status"] != "failed" {
		t.Fatalf("status = %v, want failed", payload["subsonic-response"]["status"])
	}
}
