package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestWebClientBundle(t *testing.T) {
	data, err := webClientFS.ReadFile("web-client/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, ref := range regexp.MustCompile(`(?:src|href)="(/listen/[^"]+)"`).FindAllStringSubmatch(html, -1) {
		if _, err := webClientFS.ReadFile("web-client/" + strings.TrimPrefix(ref[1], "/listen/")); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(html, `type="module"`) || !strings.Contains(html, `id="root"`) {
		t.Fatal("missing browser entry point")
	}
	s := &Server{}
	rec := httptest.NewRecorder()
	s.webClientHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/listen/index.html", nil))
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("index redirect status %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.webClientHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/listen/not-an-asset.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset status %d", rec.Code)
	}
	policy := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "script-src 'self';") || !strings.Contains(policy, "media-src 'self' blob:") {
		t.Fatalf("bad client policy: %s", policy)
	}
}

// Add to Home Screen: the page links the manifest and an apple-touch-icon,
// the manifest is served as one, and every icon it names is in the bundle.
func TestWebClientHomeScreenManifest(t *testing.T) {
	page, err := webClientFS.ReadFile("web-client/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`rel="manifest" href="/listen/manifest.webmanifest"`, `rel="apple-touch-icon"`, `name="apple-mobile-web-app-capable"`, `viewport-fit=cover`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("index.html lacks %s", want)
		}
	}

	s := &Server{}
	rec := httptest.NewRecorder()
	s.webClientHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/listen/manifest.webmanifest", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/manifest+json" {
		t.Fatalf("manifest content type %q", got)
	}
	var manifest struct {
		StartURL string `json:"start_url"`
		Scope    string `json:"scope"`
		Display  string `json:"display"`
		Icons    []struct {
			Src string `json:"src"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.StartURL != "/listen/" || manifest.Display != "standalone" {
		t.Fatalf("manifest opens %q as %q", manifest.StartURL, manifest.Display)
	}
	// /login is outside /listen/; a narrower scope would send the sign-in out
	// of the installed app into a browser tab.
	if manifest.Scope != "/" {
		t.Fatalf("manifest scope %q", manifest.Scope)
	}
	if len(manifest.Icons) == 0 {
		t.Fatal("manifest names no icons")
	}
	for _, icon := range manifest.Icons {
		if _, err := webClientFS.ReadFile("web-client/" + icon.Src); err != nil {
			t.Errorf("manifest icon %s: %v", icon.Src, err)
		}
	}
}
