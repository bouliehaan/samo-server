package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/radio"
	"github.com/bouliehaan/samo-server/internal/sources"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestCreatePodcastFeedRefreshesCatalog(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
  <channel>
    <title>Night Signals</title>
    <item>
      <title>Episode One</title>
      <guid>episode-1</guid>
      <enclosure url="https://cdn.example.com/ep1.mp3" type="audio/mpeg" length="1234" />
    </item>
  </channel>
</rss>`))
	}))
	defer feedServer.Close()

	catalogService := catalog.NewService(catalog.Seed{})
	handler := sourcesTestServer(t, db, catalogService)
	body := strings.NewReader(`{"url":"` + feedServer.URL + `/feed.xml"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/podcasts/feeds", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/podcasts", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var page catalog.Page[catalog.PodcastItem]
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].Podcast == nil {
		t.Fatalf("podcast page = %#v, want one rss podcast", page)
	}
}

func TestInternetRadioStationHandlersExposePublicLinks(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	handler := sourcesTestServer(t, db, catalog.NewService(catalog.Seed{}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internet-radio/stations", strings.NewReader(`{
		"name": "Static FM",
		"streamUrl": "https://radio.example.com/live.mp3",
		"tags": ["old time radio"]
	}`))
	req.Host = "samo.test"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var station internetRadioStationResponse
	if err := json.NewDecoder(rec.Body).Decode(&station); err != nil {
		t.Fatal(err)
	}
	if station.PublicStreamURL == "" || station.PlaylistURL == "" {
		t.Fatalf("station links = %#v, want public links", station)
	}

	req = httptest.NewRequest(http.MethodGet, "/internet-radio/"+station.ID+"/playlist.m3u", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, "https://radio.example.com/live.mp3") {
		t.Fatalf("playlist = %q, want stream URL", body)
	}
}

func TestInternetRadioStreamResolvesPlaylistURL(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/station.pls":
			_, _ = w.Write([]byte("[playlist]\nFile1=/live.mp3\n"))
		case "/live.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler := sourcesTestServer(t, db, catalog.NewService(catalog.Seed{}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internet-radio/stations", strings.NewReader(`{
		"name": "Playlist FM",
		"streamUrl": "`+upstream.URL+`/station.pls"
	}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var station internetRadioStationResponse
	if err := json.NewDecoder(rec.Body).Decode(&station); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodGet, "/internet-radio/"+station.ID+"/stream", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want redirect", rec.Code)
	}
	if location := rec.Header().Get("Location"); location != upstream.URL+"/live.mp3" {
		t.Fatalf("location = %q, want resolved stream", location)
	}
}

func sourcesTestServer(t *testing.T, db *sql.DB, catalogService *catalog.Service) http.Handler {
	t.Helper()
	radioService, err := radio.NewService(radio.Config{})
	if err != nil {
		t.Fatal(err)
	}
	reload := func(ctx context.Context) error {
		seed, err := catalog.LoadSeedFromDB(ctx, db)
		if err != nil {
			return err
		}
		catalogService.Replace(seed)
		return nil
	}
	return NewServer(ServerOptions{
		Catalog:       catalogService,
		Radio:         radioService,
		Sources:       sources.New(db),
		ReloadCatalog: reload,
	})
}
