package explo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func withStubMusicBrainz(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	old := musicbrainzRecordingURL
	musicbrainzRecordingURL = srv.URL + "/"
	t.Cleanup(func() {
		musicbrainzRecordingURL = old
		srv.Close()
	})
	return srv
}

func TestFetchReleaseGroupIDPrefersAlbumType(t *testing.T) {
	srv := withStubMusicBrainz(t, `{"releases":[
		{"release-group":{"id":"rg-single","primary-type":"Single"}},
		{"release-group":{"id":"rg-album","primary-type":"Album"}}
	]}`, 0)

	got, err := fetchReleaseGroupID(context.Background(), srv.Client(), "rec-1")
	if err != nil || got != "rg-album" {
		t.Fatalf("got (%q, %v), want (rg-album, nil)", got, err)
	}
}

func TestFetchReleaseGroupIDFallsBackToFirst(t *testing.T) {
	srv := withStubMusicBrainz(t, `{"releases":[{"release-group":{"id":"rg-comp","primary-type":"Compilation"}}]}`, 0)
	got, err := fetchReleaseGroupID(context.Background(), srv.Client(), "rec-1")
	if err != nil || got != "rg-comp" {
		t.Fatalf("got (%q, %v), want (rg-comp, nil)", got, err)
	}
}

func TestFetchReleaseGroupIDEmptyAndError(t *testing.T) {
	// No id -> empty, no request.
	if got, err := fetchReleaseGroupID(context.Background(), http.DefaultClient, "  "); got != "" || err != nil {
		t.Fatalf("blank id got (%q, %v)", got, err)
	}
	// No release groups -> "" (definitive: nothing to find), no error.
	srv := withStubMusicBrainz(t, `{"releases":[]}`, 0)
	if got, err := fetchReleaseGroupID(context.Background(), srv.Client(), "rec-1"); got != "" || err != nil {
		t.Fatalf("no-rg got (%q, %v), want empty/nil", got, err)
	}
	// Non-200 -> error (transient; caller should retry, not mark resolved).
	srv2 := withStubMusicBrainz(t, `{}`, http.StatusServiceUnavailable)
	if _, err := fetchReleaseGroupID(context.Background(), srv2.Client(), "rec-1"); err == nil {
		t.Fatal("expected error on 503")
	}
}
