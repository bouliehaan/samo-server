package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/users"
	"github.com/bouliehaan/samo-server/migrations"
)

func newLoginTestServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	userService := users.New(users.ServiceOptions{DB: db})
	const password = "correct-horse-battery-staple"
	if err := userService.Bootstrap(ctx, users.BootstrapInput{AdminUsername: "admin", AdminPassword: password}); err != nil {
		t.Fatal(err)
	}
	return NewServer(ServerOptions{Users: userService}), password
}

func loginRequestFrom(handler http.Handler, body, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestLoginRateLimitLocksOutAccountAfterRepeatedFailures(t *testing.T) {
	handler, password := newLoginTestServer(t)
	bad := `{"username":"admin","password":"wrong"}`

	for i := 0; i < 5; i++ {
		rec := loginRequestFrom(handler, bad, "203.0.113.1:1111")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401, body=%s", i, rec.Code, rec.Body.String())
		}
	}

	// A different source address doesn't help — the account itself is locked.
	good := `{"username":"admin","password":"` + password + `"}`
	rec := loginRequestFrom(handler, good, "198.51.100.9:2222")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status after account lockout = %d, want 429, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header on lockout response")
	}
}

func TestLoginRateLimitLocksOutAddressAcrossUsernames(t *testing.T) {
	handler, password := newLoginTestServer(t)
	const attackerAddr = "203.0.113.50:4444"

	for i := 0; i < 5; i++ {
		bad := `{"username":"nosuchuser` + string(rune('a'+i)) + `","password":"wrong"}`
		rec := loginRequestFrom(handler, bad, attackerAddr)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401, body=%s", i, rec.Code, rec.Body.String())
		}
	}

	// Same address, now trying the real account: blocked by the address key
	// even though this exact username never failed before.
	good := `{"username":"admin","password":"` + password + `"}`
	rec := loginRequestFrom(handler, good, attackerAddr)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status for locked address = %d, want 429, body=%s", rec.Code, rec.Body.String())
	}

	// The admin account itself is untouched from a clean address.
	rec = loginRequestFrom(handler, good, "198.51.100.77:5555")
	if rec.Code != http.StatusOK {
		t.Fatalf("status from clean address = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestLoginRateLimitAllowsSuccessBeforeThreshold(t *testing.T) {
	handler, password := newLoginTestServer(t)
	bad := `{"username":"admin","password":"wrong"}`
	for i := 0; i < 4; i++ {
		loginRequestFrom(handler, bad, "203.0.113.1:1111")
	}
	good := `{"username":"admin","password":"` + password + `"}`
	rec := loginRequestFrom(handler, good, "203.0.113.1:1111")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestClientAddrPrefersCloudflareHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "127.0.0.1:9999"
	req.Header.Set("X-Forwarded-For", "10.0.0.5")
	req.Header.Set("CF-Connecting-IP", "198.51.100.20")
	if got := clientAddr(req); got != "198.51.100.20" {
		t.Fatalf("clientAddr = %q, want CF-Connecting-IP value", got)
	}
}
