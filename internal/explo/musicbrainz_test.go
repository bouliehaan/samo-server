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

func TestFetchRecordingReleaseRefsPrefersAlbumType(t *testing.T) {
	srv := withStubMusicBrainz(t, `{"releases":[
		{"id":"rel-1","release-group":{"id":"rg-single","primary-type":"Single"}},
		{"id":"rel-2","release-group":{"id":"rg-album","primary-type":"Album"}}
	]}`, 0)

	refs, err := fetchRecordingReleaseRefs(context.Background(), srv.Client(), "rec-1")
	if err != nil || refs.ReleaseGroupID != "rg-album" {
		t.Fatalf("got (%q, %v), want (rg-album, nil)", refs.ReleaseGroupID, err)
	}
	// The chosen release group's own releases come first so the per-release
	// CAA rung tries the real record's pressings before other appearances.
	if len(refs.ReleaseIDs) != 2 || refs.ReleaseIDs[0] != "rel-2" || refs.ReleaseIDs[1] != "rel-1" {
		t.Fatalf("release ids = %v, want [rel-2 rel-1] (chosen-group first)", refs.ReleaseIDs)
	}
}

func TestFetchRecordingReleaseRefsFallsBackToFirst(t *testing.T) {
	srv := withStubMusicBrainz(t, `{"releases":[{"id":"rel-9","release-group":{"id":"rg-comp","primary-type":"Compilation"}}]}`, 0)
	refs, err := fetchRecordingReleaseRefs(context.Background(), srv.Client(), "rec-1")
	if err != nil || refs.ReleaseGroupID != "rg-comp" {
		t.Fatalf("got (%q, %v), want (rg-comp, nil)", refs.ReleaseGroupID, err)
	}
}

func TestFetchRecordingReleaseRefsEmptyAndError(t *testing.T) {
	// No id -> empty, no request.
	if refs, err := fetchRecordingReleaseRefs(context.Background(), http.DefaultClient, "  "); refs.ReleaseGroupID != "" || len(refs.ReleaseIDs) != 0 || err != nil {
		t.Fatalf("blank id got (%+v, %v)", refs, err)
	}
	// No release groups -> empty (definitive: nothing to find), no error.
	srv := withStubMusicBrainz(t, `{"releases":[]}`, 0)
	if refs, err := fetchRecordingReleaseRefs(context.Background(), srv.Client(), "rec-1"); refs.ReleaseGroupID != "" || err != nil {
		t.Fatalf("no-rg got (%+v, %v), want empty/nil", refs, err)
	}
	// Non-200 -> error (transient; caller should retry, not mark resolved).
	srv2 := withStubMusicBrainz(t, `{}`, http.StatusServiceUnavailable)
	if _, err := fetchRecordingReleaseRefs(context.Background(), srv2.Client(), "rec-1"); err == nil {
		t.Fatal("expected error on 503")
	}
}
