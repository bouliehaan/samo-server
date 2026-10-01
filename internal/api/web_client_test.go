package api

import (
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
