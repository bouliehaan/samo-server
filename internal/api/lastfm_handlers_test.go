package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestScrobbleEventsEndpointRequiresLastFM(t *testing.T) {
	handler := catalogTestServer(t, catalog.Seed{})
	body, _ := json.Marshal(lastfm.ScrobbleEventInput{TrackID: "track-1", Event: "start"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scrobble/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestLastFMStatusWhenConfigured(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	handler := NewServer(ServerOptions{
		LastFM: lastfm.NewService(lastfm.ServiceOptions{
			DB:           db,
			APIKey:       "key",
			SharedSecret: "secret",
		}),
		Playback: playback.New(db),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/lastfm/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
