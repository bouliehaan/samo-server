package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/radio"
)

func TestRadioAPIRequiresTokenWhenConfigured(t *testing.T) {
	handler := testServer(t, "secret")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/radio/stations", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/radio/stations", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRadioPlaylistEndpoint(t *testing.T) {
	handler := testServer(t, "")

	req := httptest.NewRequest(http.MethodGet, "/radio/late-night/playlist.m3u", nil)
	req.Host = "samo.test"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "audio/x-mpegurl") {
		t.Fatalf("content type = %q, want audio/x-mpegurl", contentType)
	}
	if body := rec.Body.String(); !strings.Contains(body, "http://samo.test/radio/late-night/stream") {
		t.Fatalf("playlist body = %q, want stream URL", body)
	}
}

func testServer(t *testing.T, token string) http.Handler {
	t.Helper()

	service, err := radio.NewService(radio.Config{Stations: []radio.StationConfig{{
		ID:    "late-night",
		Name:  "Late Night",
		Epoch: "2026-01-01T00:00:00Z",
		Media: []radio.MediaItemConfig{
			{ID: "show", Title: "Show", Path: "/tmp/show.mp3", DurationSeconds: 3600},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	return NewServer(ServerOptions{
		APIToken: token,
		Radio:    service,
	})
}
