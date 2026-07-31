package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

func newIdentityTestServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	ctx := context.Background()
	db := storagetest.Open(t)
	userService := users.New(users.ServiceOptions{DB: db})
	const password = "correct-horse-battery-staple"
	if err := userService.Bootstrap(ctx, users.BootstrapInput{AdminUsername: "admin", AdminPassword: password}); err != nil {
		t.Fatal(err)
	}
	return NewServer(ServerOptions{DB: db, Users: userService}), password
}

func decodeServerID(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		ServerID string `json:"serverId"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload.ServerID
}

// The health endpoint is the probe a client uses to decide whether an address
// is reachable AND is the server it already knows, so the ID has to be there.
func TestHealthReportsServerIdentity(t *testing.T) {
	handler, _ := newIdentityTestServer(t)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
	id := decodeServerID(t, rec.Body.Bytes())
	if !strings.HasPrefix(id, "srv-") {
		t.Fatalf("expected a srv- prefixed identity, got %q", id)
	}
}

func TestHealthIdentityIsStableAcrossRequests(t *testing.T) {
	handler, _ := newIdentityTestServer(t)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/health", nil))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/health", nil))

	if a, b := decodeServerID(t, first.Body.Bytes()), decodeServerID(t, second.Body.Bytes()); a != b {
		t.Fatalf("identity changed between requests: %q then %q", a, b)
	}
}

// Login is where a client first learns the identity, and it must agree with
// what the probe endpoint reports or the client would key its data by one
// value and verify against another.
func TestLoginReturnsSameServerIdentityAsHealth(t *testing.T) {
	handler, password := newIdentityTestServer(t)

	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, httptest.NewRequest(http.MethodGet, "/health", nil))
	fromHealth := decodeServerID(t, healthRec.Body.Bytes())

	body := `{"username":"admin","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, req)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d (%s)", loginRec.Code, loginRec.Body.String())
	}
	fromLogin := decodeServerID(t, loginRec.Body.Bytes())

	if fromLogin != fromHealth {
		t.Fatalf("login reported %q but health reported %q", fromLogin, fromHealth)
	}
}

// The identity is additive: the login payload must still carry everything an
// existing client already reads.
func TestLoginResponseStillCarriesCredential(t *testing.T) {
	handler, password := newIdentityTestServer(t)

	body := `{"username":"admin","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var payload struct {
		Token string `json:"token"`
		User  struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if payload.Token == "" {
		t.Fatal("login response lost its token")
	}
	if payload.User.Username != "admin" {
		t.Fatalf("login response lost its user: %q", payload.User.Username)
	}
}

// A server without a database still answers health probes; it just cannot
// advertise an identity, and the field is omitted rather than sent empty.
func TestHealthOmitsIdentityWithoutDatabase(t *testing.T) {
	handler := NewServer(ServerOptions{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "serverId") {
		t.Fatalf("expected serverId to be omitted, got %s", rec.Body.String())
	}
}
