package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

// newPairingTestServer is a server with an admin account, past first-run
// setup (a library, scanned) unless finishSetup is false.
func newPairingTestServer(t *testing.T, finishSetup bool) (http.Handler, string) {
	t.Helper()
	ctx := context.Background()
	db := storagetest.Open(t)
	userService := users.New(users.ServiceOptions{DB: db})
	const password = "correct-horse-battery-staple"
	if err := userService.Bootstrap(ctx, users.BootstrapInput{AdminUsername: "admin", AdminPassword: password}); err != nil {
		t.Fatal(err)
	}
	libraryService := libraries.New(db, scanner.New(db))
	if finishSetup {
		if _, err := libraryService.Create(ctx, libraries.CreateLibraryInput{Name: "Music", Kind: "music", Path: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		if _, err := libraryService.ScanAll(ctx, libraries.TriggerStartup, ""); err != nil {
			t.Fatal(err)
		}
	}
	return NewServer(ServerOptions{DB: db, Users: userService, Libraries: libraryService}), password
}

// A code nobody can approve is a dead end, and /pair's sign-in would bounce to
// /setup and lose it. Mid-setup the TV is told to finish setup instead.
func TestDevicePairingWaitsForSetup(t *testing.T) {
	handler, _ := newPairingTestServer(t, false)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/device/start", bytes.NewBufferString("{}")))
	if rec.Code != http.StatusServiceUnavailable || bytes.Contains(rec.Body.Bytes(), []byte("device_code")) {
		t.Fatalf("started pairing mid-setup: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDevicePairingPendingApproveConsume(t *testing.T) {
	p := newDevicePairings()
	now := time.Now()
	secret, code, err := p.start("tv", now)
	if err != nil || len(secret) != 64 || len(code) != 9 {
		t.Fatalf("invalid challenge: %v", err)
	}
	if _, status, _ := p.poll(secret, now); status != "authorization_pending" {
		t.Fatal(status)
	}
	if status := p.decide(code, "listener", true, now); status != "approved" {
		t.Fatal(status)
	}
	if status := p.decide(code, "attacker", true, now); status != "invalid_code" {
		t.Fatal("reapproval", status)
	}
	user, status, _ := p.poll(secret, now.Add(pairingInterval))
	if status != "approved" || user != "listener" {
		t.Fatal(user, status)
	}
	if _, status, _ := p.poll(secret, now.Add(2*pairingInterval)); status != "expired_token" {
		t.Fatal("replay", status)
	}
}
func TestDevicePairingExpiryDenialAndSlowDown(t *testing.T) {
	p := newDevicePairings()
	now := time.Now()
	secret, code, _ := p.start("tv", now)
	p.poll(secret, now)
	if _, status, interval := p.poll(secret, now); status != "slow_down" || interval != 10*time.Second {
		t.Fatal(status, interval)
	}
	if p.decide(code, "listener", false, now) != "approved" {
		t.Fatal("decline failed")
	}
	if _, status, _ := p.poll(secret, now.Add(10*time.Second)); status != "access_denied" {
		t.Fatal(status)
	}
	secret, code, _ = p.start("tv", now)
	if p.decide(code, "listener", true, now.Add(pairingLifetime)) != "invalid_code" {
		t.Fatal("expired approval accepted")
	}
	if _, status, _ := p.poll(secret, now.Add(pairingLifetime)); status != "expired_token" {
		t.Fatal(status)
	}
}
func TestDevicePairingDisplayCodeCannotRedeem(t *testing.T) {
	p := newDevicePairings()
	now := time.Now()
	_, code, _ := p.start("tv", now)
	p.decide(code, "listener", true, now)
	if _, status, _ := p.poll(code, now); status != "expired_token" {
		t.Fatal(status)
	}
}
func TestDevicePairingConcurrentRedemption(t *testing.T) {
	p := newDevicePairings()
	now := time.Now()
	secret, code, _ := p.start("tv", now)
	p.decide(code, "listener", true, now)
	results := make(chan string, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, status, _ := p.poll(secret, now); results <- status }()
	}
	wg.Wait()
	close(results)
	approved := 0
	for result := range results {
		if result == "approved" {
			approved++
		}
	}
	if approved != 1 {
		t.Fatalf("redeemed %d times", approved)
	}
}
func TestDevicePairingRateLimitsAndCleanup(t *testing.T) {
	p := newDevicePairings()
	now := time.Now()
	for i := 0; i < 10; i++ {
		if _, _, err := p.start("tv", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := p.start("tv", now); err == nil {
		t.Fatal("missing start limit")
	}
	for i := 0; i < 20; i++ {
		p.decide("wrong", "listener", true, now)
	}
	if p.decide("wrong", "listener", true, now) != "slow_down" {
		t.Fatal("missing approval limit")
	}
	if _, _, err := p.start("tv", now.Add(pairingLifetime)); err != nil {
		t.Fatal(err)
	}
	if len(p.sessions) != 1 {
		t.Fatal("expired sessions retained")
	}
}

func TestDevicePairingHTTPAuthorization(t *testing.T) {
	handler, password := newPairingTestServer(t, true)
	request := func(path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		handler.ServeHTTP(rec, req)
		return rec
	}
	start := request("/api/v1/auth/device/start", "{}", "")
	if start.Code != 200 || start.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(start.Code, start.Body.String())
	}
	var challenge struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &challenge); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"user_code": challenge.UserCode, "approve": true})
	if rec := request("/api/v1/auth/device/approve", string(body), ""); rec.Code != 401 {
		t.Fatal("anonymous approval", rec.Code)
	}
	login := request("/api/v1/auth/login", `{"username":"admin","password":"`+password+`"}`, "")
	var auth struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &auth); err != nil || auth.Token == "" {
		t.Fatal("login failed", login.Body.String())
	}
	stream := request("/api/v1/auth/stream-token", "{}", auth.Token)
	var streamAuth struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(stream.Body.Bytes(), &streamAuth)
	if rec := request("/api/v1/auth/device/approve?stream_token="+streamAuth.Token, string(body), ""); rec.Code != 401 {
		t.Fatal("stream token approved", rec.Code)
	}
	approved := request("/api/v1/auth/device/approve", string(body), auth.Token)
	if approved.Code != 200 {
		t.Fatal(approved.Code, approved.Body.String())
	}
	pollBody, _ := json.Marshal(map[string]string{"device_code": challenge.DeviceCode})
	result := request("/api/v1/auth/device/poll", string(pollBody), "")
	// The approval must be a login body (plus status): the TV builds its
	// session from it with the same code a password login uses.
	var issued struct {
		Status    string `json:"status"`
		Token     string `json:"token"`
		ServerID  string `json:"serverId"`
		TokenMeta struct {
			Label string `json:"label"`
		} `json:"tokenMeta"`
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &issued); err != nil || issued.Status != "approved" || issued.Token == "" || issued.Token == auth.Token || issued.ServerID == "" {
		t.Fatal("invalid device token", result.Code)
	}
	if issued.User.ID == "" || issued.User.Username != "admin" || issued.TokenMeta.Label != "samo Android TV" {
		t.Fatalf("approval is not a login body: %s", result.Body.String())
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+issued.Token)
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal("device credential rejected", rec.Code)
	}
	replay := request("/api/v1/auth/device/poll", string(pollBody), "")
	var repeat map[string]any
	_ = json.Unmarshal(replay.Body.Bytes(), &repeat)
	if repeat["status"] != "expired_token" || repeat["token"] != nil {
		t.Fatal("replayed credential")
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/pair", nil))
	if page.Code != 200 || bytes.Contains(page.Body.Bytes(), []byte("__SAMO_")) {
		t.Fatal("invalid pairing page")
	}
}
