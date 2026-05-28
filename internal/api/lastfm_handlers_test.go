package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func TestLastFMConfigCanBeSavedViaAPI(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := lastfm.NewService(lastfm.ServiceOptions{
		DB: db,
		HTTPClient: &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if err := req.ParseForm(); err != nil {
					return nil, err
				}
				if req.FormValue("method") == "auth.getToken" {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"token":"ok"}`)),
						Request:    req,
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"message":"unexpected method","error":0}`)),
					Request:    req,
				}, nil
			}),
		},
	})
	handler := NewServer(ServerOptions{
		LastFM:   service,
		Playback: playback.New(db),
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/lastfm/config", strings.NewReader(`{
		"apiKey": "key",
		"sharedSecret": "secret"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !service.Enabled() {
		t.Fatal("service should be enabled after saving config")
	}
	if !strings.Contains(rec.Body.String(), `"source":"ui"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/lastfm/config", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if service.Enabled() {
		t.Fatal("service should be disabled after clearing config")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
