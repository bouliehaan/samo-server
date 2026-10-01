package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"explo/src/web/backend/songsearch"
	"github.com/bouliehaan/samo-server/internal/api"
	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// Exercise the real implementations from both checkouts. No manual callback
// URL/token, downloader process, remote music service, or deployed server is used.
func TestExistingSetupConnectsAndGatesSearch(t *testing.T) {
	db := storagetest.Open(t)
	pipeline := explo.NewService(explo.ServiceOptions{DB: db, Dirs: []string{t.TempDir()}, AcoustIDAPIKey: "test-key", FpcalcPath: "/test/fpcalc"})
	newHandler := func() http.Handler {
		return api.NewServer(api.ServerOptions{DB: db, Explo: pipeline, APIToken: "existing-admin-token"})
	}
	samo := httptest.NewServer(newHandler())
	defer samo.Close()
	t.Setenv("EXPLO_SYSTEM", "samo")
	t.Setenv("SYSTEM_URL", samo.URL)
	t.Setenv("API_KEY", "existing-admin-token")
	t.Setenv("DOWNLOAD_SERVICES", "slskd")
	t.Setenv("SLSKD_URL", "http://unused.invalid")
	t.Setenv("SLSKD_API_KEY", "test-provider-key")
	t.Setenv("MIGRATE_DOWNLOADS", "true")
	t.Setenv("DOWNLOAD_DIR", t.TempDir())
	service := songsearch.New("", filepath.Join(t.TempDir(), "missing.env"))
	mux := http.NewServeMux()
	service.Register(mux)
	companion := httptest.NewServer(mux)
	defer companion.Close()
	endpoint, _ := url.Parse(companion.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Connect(ctx, endpoint.Host)

	status := func(handler http.Handler) bool {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/explo/discovery/status", nil)
		req.Header.Set("Authorization", "Bearer existing-admin-token")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		var state struct{ Available bool }
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &state) != nil {
			t.Fatalf("status: %d %s", res.Code, res.Body.String())
		}
		return state.Available
	}
	handler := newHandler()
	deadline := time.Now().Add(5 * time.Second)
	for !status(handler) {
		if time.Now().After(deadline) {
			t.Fatal("existing credentials failed to establish connection")
		}
		time.Sleep(25 * time.Millisecond)
	}
	// A recreated Samo handler resolves the persisted connection without options.
	if !status(newHandler()) {
		t.Fatal("connection lost across Samo restart")
	}
	// Reach the actual Explo route with its derived credential through Samo.
	req := httptest.NewRequest("GET", "/api/v1/explo/downloads/12345678-1234-1234-1234-123456789012", nil)
	req.Header.Set("Authorization", "Bearer existing-admin-token")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 404 {
		t.Fatalf("real Explo route/auth failed: %d %s", res.Code, res.Body.String())
	}
	companion.Close()
	if status(handler) {
		t.Fatal("search still available after Explo disconnected")
	}
	req = httptest.NewRequest("GET", "/api/v1/explo/search?q=song", nil)
	req.Header.Set("Authorization", "Bearer existing-admin-token")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 503 {
		t.Fatal("disconnected search was not gated")
	}
}
