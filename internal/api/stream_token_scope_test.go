package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

// A stream token rides in URLs, so it ends up in access logs, Referer headers
// and proxies. These pin what one opens when it is the only credential a
// request carries: media bytes, and nothing else.

// mintStreamToken mints a stream token the way every client does, with a bearer.
func mintStreamToken(t *testing.T, handler http.Handler, bearer string) string {
	t.Helper()
	rec := doRequest(handler, http.MethodPost, "/api/v1/auth/stream-token", "{}", bearer)
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Token == "" {
		t.Fatalf("stream token mint: %d %s", rec.Code, rec.Body.String())
	}
	return body.Token
}

func withStreamToken(path, token string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "stream_token=" + url.QueryEscape(token)
}

func listTokenLabels(t *testing.T, handler http.Handler, bearer string) map[string]string {
	t.Helper()
	rec := doRequest(handler, http.MethodGet, "/api/v1/users/me/tokens", "", bearer)
	if rec.Code != http.StatusOK {
		t.Fatalf("list tokens: %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []users.Token `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, item := range list.Items {
		labels[item.Label] = item.ID
	}
	return labels
}

// An admin's stream token, leaked from a log, is the worst case: it must not
// reach any account, credential or user-management route, and none of what
// those routes do may happen.
func TestAccountRoutesRefuseStreamTokens(t *testing.T) {
	handler, password := newLoginTestServer(t)
	loginToken, deviceToken := signInForTokens(t, handler, password)
	stream := mintStreamToken(t, handler, deviceToken)
	before := listTokenLabels(t, handler, deviceToken)
	loginTokenID := before["login"]
	if loginTokenID == "" || before["samo client"] == "" {
		t.Fatalf("tokens before = %v, want the login and device tokens", before)
	}

	for _, route := range []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/users/me", ""},
		{http.MethodPatch, "/api/v1/users/me", `{"password":"stolen-password","displayName":"stolen"}`},
		{http.MethodGet, "/api/v1/users/me/tokens", ""},
		{http.MethodPost, "/api/v1/users/me/tokens", `{"label":"stolen"}`},
		{http.MethodDelete, "/api/v1/users/me/tokens/" + loginTokenID, ""},
		{http.MethodDelete, "/api/v1/users/me/tokens/current", ""},
		{http.MethodGet, "/api/v1/users/me/subsonic", ""},
		{http.MethodPost, "/api/v1/users/me/subsonic", ""},
		{http.MethodDelete, "/api/v1/users/me/subsonic", ""},
		{http.MethodGet, "/api/v1/users", ""},
		{http.MethodPost, "/api/v1/users", `{"username":"intruder","password":"intruder-pass","role":"admin"}`},
		// A stream token that could mint its successor would never expire.
		{http.MethodPost, "/api/v1/auth/stream-token", "{}"},
		// Pairing mints a permanent admin token and sends it to the device's
		// address, which the create route sets.
		{http.MethodPost, "/api/v1/samo-radio/devices", `{"name":"kitchen","address":"http://203.0.113.9:7777"}`},
		{http.MethodPost, "/api/v1/samo-radio/devices/any/pair", ""},
		// Not only account routes: the default is a bearer.
		{http.MethodGet, "/api/v1/music/albums", ""},
		{http.MethodGet, "/api/v1/events", ""},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			rec := doRequest(handler, route.method, withStreamToken(route.path, stream), route.body, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("401 without WWW-Authenticate")
			}
		})
	}

	// None of it happened.
	after := listTokenLabels(t, handler, loginToken)
	if len(after) != len(before) || after["login"] != loginTokenID || after["samo client"] != before["samo client"] {
		t.Fatalf("tokens after = %v, want them unchanged from %v", after, before)
	}
	if rec := loginRequestFrom(handler, `{"username":"admin","password":"`+password+`"}`, "198.51.100.1:1"); rec.Code != http.StatusOK {
		t.Fatalf("the original password stopped working: %d", rec.Code)
	}
	if rec := loginRequestFrom(handler, `{"username":"admin","password":"stolen-password"}`, "198.51.100.2:1"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the password a stream token sent was set: %d", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, "/api/v1/users", "", loginToken); strings.Contains(rec.Body.String(), "intruder") {
		t.Fatalf("a stream token created a user: %s", rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodGet, "/api/v1/users/me/subsonic", "", loginToken); !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("a stream token set a Subsonic password: %s", rec.Body.String())
	}
}

// A real bearer is either header, and it still reaches everything. A stream
// token riding along beside it changes nothing.
func TestAccountRoutesTakeEitherBearerHeader(t *testing.T) {
	handler, password := newLoginTestServer(t)
	_, deviceToken := signInForTokens(t, handler, password)
	stream := mintStreamToken(t, handler, deviceToken)

	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("X-Samo-Token", deviceToken)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := request(http.MethodGet, "/api/v1/users/me", ""); rec.Code != http.StatusOK {
		t.Fatalf("X-Samo-Token users/me = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPost, "/api/v1/users/me/tokens", `{"label":"second device"}`); rec.Code != http.StatusCreated {
		t.Fatalf("X-Samo-Token mint = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodGet, withStreamToken("/api/v1/users/me/tokens", stream), "", deviceToken); rec.Code != http.StatusOK {
		t.Fatalf("bearer plus stream token = %d, body=%s", rec.Code, rec.Body.String())
	}
}

// The setup wizard's later steps authenticate themselves, and take the
// step-one admin bearer, not a stream token minted from it.
func TestSetupStepsRefuseStreamTokens(t *testing.T) {
	db := storagetest.Open(t)
	handler := NewServer(ServerOptions{
		Libraries: libraries.New(db, scanner.New(db)),
		Users:     users.New(users.ServiceOptions{DB: db}),
	})
	rec := doRequest(handler, http.MethodPost, "/api/v1/setup/admin", `{"username":"admin","password":"samo-rocks-12345"}`, "")
	var login users.LoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil || login.Token == "" {
		t.Fatalf("create admin: %d %s", rec.Code, rec.Body.String())
	}
	stream := mintStreamToken(t, handler, login.Token)

	libraryDir := filepath.Join(t.TempDir(), "music")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"Music","kind":"music","path":"` + libraryDir + `"}`
	if rec := doRequest(handler, http.MethodPost, withStreamToken("/api/v1/setup/libraries", stream), body, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stream-token setup step = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodPost, "/api/v1/setup/libraries", body, login.Token); rec.Code != http.StatusCreated {
		t.Fatalf("bearer setup step = %d, body=%s", rec.Code, rec.Body.String())
	}
}

// What a stream token is for: a URL that plays or shows something, opened by
// a consumer that cannot send a header.
func TestMediaRoutesAcceptStreamTokens(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	libraryDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	audio := []byte("0123456789")
	audioPath := filepath.Join(libraryDir, "song.flac")
	if err := os.WriteFile(audioPath, audio, 0o644); err != nil {
		t.Fatal(err)
	}
	coverPath := filepath.Join(libraryDir, "cover.jpg")
	cover, err := os.Create(coverPath)
	if err != nil {
		t.Fatal(err)
	}
	art := image.NewRGBA(image.Rect(0, 0, 8, 8))
	art.Set(0, 0, color.RGBA{R: 200, A: 255})
	if err := jpeg.Encode(cover, art, nil); err != nil {
		t.Fatal(err)
	}
	cover.Close()

	db := storagetest.Open(t)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path)
		VALUES ('library-1', 'Music', 'music', '', ?)`, libraryDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, path, file_name, mime_type, size_bytes, duration_seconds)
		VALUES ('file-1', 'library-1', ?, 'song.flac', 'audio/flac', ?, 1)`, audioPath, len(audio)); err != nil {
		t.Fatal(err)
	}
	userService := users.New(users.ServiceOptions{DB: db})
	const password = "correct-horse-battery-staple"
	if err := userService.Bootstrap(ctx, users.BootstrapInput{AdminUsername: "admin", AdminPassword: password}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{
		Users: userService,
		Files: files.New(db),
		Catalog: catalog.NewService(catalog.Seed{
			MusicAlbums: []catalog.MusicAlbum{{
				ID:     "album-1",
				Title:  "Test Album",
				Images: []catalog.Image{{ID: "image_cover1", Path: coverPath}},
			}},
		}),
	})
	_, deviceToken := signInForTokens(t, handler, password)
	stream := mintStreamToken(t, handler, deviceToken)

	rec := doRequest(handler, http.MethodGet, withStreamToken("/api/v1/media/files/file-1/stream", stream), "", "")
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), audio) {
		t.Fatalf("stream with a stream token = %d %q", rec.Code, rec.Body.Bytes())
	}
	for _, path := range []string{"/api/v1/music/albums/album-1/cover", "/api/v1/media/images/image_cover1/image"} {
		if rec := doRequest(handler, http.MethodGet, withStreamToken(path, stream), "", ""); rec.Code != http.StatusOK {
			t.Fatalf("%s with a stream token = %d, body=%s", path, rec.Code, rec.Body.String())
		}
	}

	// The file's metadata is not media bytes, and a token that is not real
	// opens nothing.
	if rec := doRequest(handler, http.MethodGet, withStreamToken("/api/v1/media/files/file-1", stream), "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("media metadata with a stream token = %d, want 401", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, withStreamToken("/api/v1/media/files/file-1/stream", "smt_not_real"), "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stream with an unknown stream token = %d, want 401", rec.Code)
	}
}
