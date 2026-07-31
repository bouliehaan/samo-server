package subsonic_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/subsonic"
	"github.com/bouliehaan/samo-server/internal/users"
)

// The Subsonic surface carries its own authentication, separate from the native
// bearer scheme, and it is reachable by anything that can hit the port. These
// pin the two things that must never regress: valid credentials work, and
// everything else is refused — including on the streaming route, where a leak
// means serving a user's media to an anonymous caller.

func newTestServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	db := storagetest.Open(t)
	ctx := context.Background()

	userService := users.New(users.ServiceOptions{DB: db, ReadDB: db})
	if _, err := userService.BootstrapWithResult(ctx, users.BootstrapInput{
		AdminUsername: "listener",
		AdminPassword: "login-password-123",
	}); err != nil {
		t.Fatal(err)
	}
	principal, err := userService.AuthenticateCredentials(ctx, "listener", "login-password-123")
	if err != nil {
		t.Fatal(err)
	}
	subsonicPassword, err := userService.GenerateSubsonicPassword(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	subsonic.New(subsonic.Options{
		Catalog: stubCatalog{},
		Users:   userService,
	}).Register(mux)
	return mux, subsonicPassword
}

func token(password, salt string) string {
	sum := md5.Sum([]byte(password + salt))
	return hex.EncodeToString(sum[:])
}

func callStatus(t *testing.T, handler http.Handler, url string) (string, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))

	var payload struct {
		Response struct {
			Status string `json:"status"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		} `json:"subsonic-response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %s: %v (body %q)", url, err, rec.Body.String())
	}
	code := 0
	if payload.Response.Error != nil {
		code = payload.Response.Error.Code
	}
	return payload.Response.Status, code
}

func TestTokenSaltAuthAccepted(t *testing.T) {
	handler, password := newTestServer(t)
	salt := "somesalt"
	status, _ := callStatus(t, handler,
		"/rest/ping.view?u=listener&t="+token(password, salt)+"&s="+salt+"&v=1.16.1&c=test&f=json")
	if status != "ok" {
		t.Fatalf("valid token+salt rejected: status=%s", status)
	}
}

func TestPlaintextPasswordAccepted(t *testing.T) {
	handler, password := newTestServer(t)
	status, _ := callStatus(t, handler,
		"/rest/ping.view?u=listener&p="+password+"&v=1.16.1&c=test&f=json")
	if status != "ok" {
		t.Fatalf("valid plaintext password rejected: status=%s", status)
	}
}

func TestHexEncodedPasswordAccepted(t *testing.T) {
	handler, password := newTestServer(t)
	enc := "enc:" + hex.EncodeToString([]byte(password))
	status, _ := callStatus(t, handler,
		"/rest/ping.view?u=listener&p="+enc+"&v=1.16.1&c=test&f=json")
	if status != "ok" {
		t.Fatalf("enc: password rejected: status=%s", status)
	}
}

// ping is what a client calls to validate credentials during setup. If it does
// not authenticate, a wrong password looks like a working connection and fails
// mysteriously later — which is exactly the bug this caught.
func TestPingRejectsWrongPassword(t *testing.T) {
	handler, _ := newTestServer(t)
	salt := "somesalt"
	status, code := callStatus(t, handler,
		"/rest/ping.view?u=listener&t="+token("wrong-password", salt)+"&s="+salt+"&v=1.16.1&c=test&f=json")
	if status != "failed" || code != 40 {
		t.Fatalf("wrong password accepted: status=%s code=%d", status, code)
	}
}

func TestUnknownUserRejected(t *testing.T) {
	handler, password := newTestServer(t)
	status, code := callStatus(t, handler,
		"/rest/ping.view?u=nobody&p="+password+"&v=1.16.1&c=test&f=json")
	if status != "failed" || code != 40 {
		t.Fatalf("unknown user accepted: status=%s code=%d", status, code)
	}
}

func TestMissingCredentialsRejected(t *testing.T) {
	handler, _ := newTestServer(t)
	for _, path := range []string{
		"/rest/getArtists?v=1.16.1&c=test&f=json",
		"/rest/getAlbumList2?v=1.16.1&c=test&f=json",
		"/rest/search3?v=1.16.1&c=test&f=json&query=x",
	} {
		status, code := callStatus(t, handler, path)
		if status != "failed" || code != 40 {
			t.Errorf("%s served without credentials: status=%s code=%d", path, status, code)
		}
	}
}

// The route that actually leaks media if auth is wrong.
func TestStreamRefusesWithoutCredentials(t *testing.T) {
	handler, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/rest/stream?v=1.16.1&c=test&f=json&id=track_anything", nil))

	if contentType := rec.Header().Get("Content-Type"); contentType == "audio/mpeg" {
		t.Fatal("stream served audio without credentials")
	}
	status, code := callStatus(t, handler, "/rest/stream?v=1.16.1&c=test&f=json&id=track_anything")
	if status != "failed" || code != 40 {
		t.Fatalf("unauthenticated stream not refused: status=%s code=%d", status, code)
	}
}

// A revoked Subsonic credential must stop working immediately, without
// affecting the account's real login.
func TestRevokedCredentialStopsWorking(t *testing.T) {
	db := storagetest.Open(t)
	ctx := context.Background()
	userService := users.New(users.ServiceOptions{DB: db, ReadDB: db})
	if _, err := userService.BootstrapWithResult(ctx, users.BootstrapInput{
		AdminUsername: "listener", AdminPassword: "login-password-123",
	}); err != nil {
		t.Fatal(err)
	}
	principal, err := userService.AuthenticateCredentials(ctx, "listener", "login-password-123")
	if err != nil {
		t.Fatal(err)
	}
	password, err := userService.GenerateSubsonicPassword(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	subsonic.New(subsonic.Options{Catalog: stubCatalog{}, Users: userService}).Register(mux)

	salt := "salt"
	url := "/rest/ping.view?u=listener&t=" + token(password, salt) + "&s=" + salt + "&v=1.16.1&c=test&f=json"
	if status, _ := callStatus(t, mux, url); status != "ok" {
		t.Fatal("credential should work before revocation")
	}

	if err := userService.ClearSubsonicPassword(ctx, principal); err != nil {
		t.Fatal(err)
	}
	if status, code := callStatus(t, mux, url); status != "failed" || code != 40 {
		t.Fatalf("revoked credential still works: status=%s code=%d", status, code)
	}
	// The real login must be unaffected.
	if _, err := userService.AuthenticateCredentials(ctx, "listener", "login-password-123"); err != nil {
		t.Fatalf("revoking the Subsonic credential broke the account login: %v", err)
	}
}
