package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

// signInForTokens logs in and mints a device token the way a samo client
// does, returning the login token and the device token.
func signInForTokens(t *testing.T, handler http.Handler, password string) (string, string) {
	t.Helper()
	login := doRequest(handler, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"`+password+`"}`, "")
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.Token == "" {
		t.Fatalf("login failed: %d %s", login.Code, login.Body.String())
	}
	minted := doRequest(handler, http.MethodPost, "/api/v1/users/me/tokens", `{"label":"samo client"}`, session.Token)
	var device struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(minted.Body.Bytes(), &device); err != nil || device.Secret == "" {
		t.Fatalf("device token failed: %d %s", minted.Code, minted.Body.String())
	}
	return session.Token, device.Secret
}

// Signing out retires the token the client holds and nothing else: the
// account's other credentials keep working.
func TestRevokeCurrentTokenRetiresOnlyThePresentedToken(t *testing.T) {
	handler, password := newLoginTestServer(t)
	loginToken, deviceToken := signInForTokens(t, handler, password)

	revoked := doRequest(handler, http.MethodDelete, "/api/v1/users/me/tokens/current", "", deviceToken)
	if revoked.Code != http.StatusOK {
		t.Fatalf("sign-out status = %d, body=%s", revoked.Code, revoked.Body.String())
	}
	if rec := doRequest(handler, http.MethodGet, "/api/v1/users/me", "", deviceToken); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token still authenticates: %d", rec.Code)
	}
	if rec := doRequest(handler, http.MethodDelete, "/api/v1/users/me/tokens/current", "", deviceToken); rec.Code != http.StatusUnauthorized {
		t.Fatalf("second sign-out status = %d, want 401", rec.Code)
	}

	list := doRequest(handler, http.MethodGet, "/api/v1/users/me/tokens", "", loginToken)
	if list.Code != http.StatusOK {
		t.Fatalf("other token stopped working: %d", list.Code)
	}
	var tokens struct {
		Items []users.Token `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}
	if len(tokens.Items) != 1 || tokens.Items[0].Label != "login" {
		t.Fatalf("tokens after sign-out = %+v, want only the login token", tokens.Items)
	}
}

// A stream token rides in URLs, so it can end up in a log. It authenticates
// ordinary requests, but it must not be able to sign a device out.
func TestRevokeCurrentTokenIgnoresStreamTokens(t *testing.T) {
	handler, password := newLoginTestServer(t)
	_, deviceToken := signInForTokens(t, handler, password)

	minted := doRequest(handler, http.MethodPost, "/api/v1/auth/stream-token", "{}", deviceToken)
	var stream struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(minted.Body.Bytes(), &stream); err != nil || stream.Token == "" {
		t.Fatalf("stream token failed: %d %s", minted.Code, minted.Body.String())
	}

	rec := doRequest(handler, http.MethodDelete, "/api/v1/users/me/tokens/current?stream_token="+stream.Token, "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stream-token sign-out status = %d, want 401", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, "/api/v1/users/me", "", deviceToken); rec.Code != http.StatusOK {
		t.Fatalf("a stream token revoked its bearer: %d", rec.Code)
	}
}

// SAMO_API_TOKEN is one secret shared by every legacy client and script of an
// install; one of them disconnecting must not cut off the rest. Only that row
// is protected: other tokens on the reserved account sign out normally.
func TestRevokeCurrentTokenRefusesTheSharedServerToken(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	const shared = "legacy-shared-secret"
	configured := users.New(users.ServiceOptions{DB: db, LegacyAPIToken: shared})
	if err := configured.Bootstrap(ctx, users.BootstrapInput{AdminUsername: "admin", AdminPassword: "admin-pass"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{Users: configured})

	if rec := doRequest(handler, http.MethodDelete, "/api/v1/users/me/tokens/current", "", shared); rec.Code != http.StatusForbidden {
		t.Fatalf("shared token sign-out status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodGet, "/api/v1/users/me", "", shared); rec.Code != http.StatusOK {
		t.Fatalf("shared token stopped working: %d", rec.Code)
	}

	device, err := configured.IssueToken(ctx, users.Principal{User: users.User{ID: users.BootstrapUserID}}, users.CreateTokenInput{Label: "samo-radio: kitchen"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := doRequest(handler, http.MethodDelete, "/api/v1/users/me/tokens/current", "", device.Secret); rec.Code != http.StatusOK {
		t.Fatalf("reserved-account device sign-out status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodGet, "/api/v1/users/me", "", shared); rec.Code != http.StatusOK {
		t.Fatalf("a device signing out took the shared token with it: %d", rec.Code)
	}

	// Taking the variable out of the environment retires the secret at the next
	// start: it authenticates nothing, sign-out included.
	unconfigured := users.New(users.ServiceOptions{DB: db})
	if err := unconfigured.Bootstrap(ctx, users.BootstrapInput{}); err != nil {
		t.Fatal(err)
	}
	handler = NewServer(ServerOptions{Users: unconfigured})
	if rec := doRequest(handler, http.MethodGet, "/api/v1/users/me", "", shared); rec.Code != http.StatusUnauthorized {
		t.Fatalf("removed shared token still authenticates: %d", rec.Code)
	}
	if rec := doRequest(handler, http.MethodDelete, "/api/v1/users/me/tokens/current", "", shared); rec.Code != http.StatusUnauthorized {
		t.Fatalf("removed shared token sign-out status = %d, want 401", rec.Code)
	}
}
