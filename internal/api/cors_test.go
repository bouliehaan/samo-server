package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSPreflightAllowsDesktopDevOrigin(t *testing.T) {
	handler := testServer(t, "")

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/setup/status", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	// Auth is bearer-token-only (no cookies), so credentialed CORS is never
	// needed and must stay off — combined with reflected origins it would let
	// any site ride an authenticated session. See WithCORS's doc comment.
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want unset", got)
	}
}

func TestCORSReflectsOriginOnAPIResponse(t *testing.T) {
	handler := testServer(t, "")

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}
