package explo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLookupAcoustIDReturnsBestMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("client") != "test-key" {
			t.Fatalf("client = %q", r.URL.Query().Get("client"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "ok",
			"results": [
				{"id": "low-score", "score": 0.4, "recordings": [{"id": "mb-low", "title": "Wrong Song", "artists": [{"name": "Wrong Artist"}]}]},
				{"id": "best", "score": 0.95, "recordings": [{
					"id": "mb-1", "title": "One More Time", "artists": [{"name": "Daft Punk"}],
					"releasegroups": [{"id": "rg-1", "title": "Homework", "type": "Single"}, {"id": "rg-2", "title": "Discovery", "type": "Album"}]
				}]}
			]
		}`))
	}))
	defer server.Close()
	orig := acoustidLookupURL
	acoustidLookupURL = server.URL
	t.Cleanup(func() { acoustidLookupURL = orig })

	match, ok, err := lookupAcoustID(context.Background(), server.Client(), "test-key", Fingerprint{DurationSeconds: 320, Value: "AQAA"})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a match")
	}
	if match.Title != "One More Time" || match.Artist != "Daft Punk" {
		t.Fatalf("match = %#v", match)
	}
	if match.MusicBrainzRecordingID != "mb-1" {
		t.Fatalf("recording id = %q", match.MusicBrainzRecordingID)
	}
	if match.Album != "Discovery" {
		t.Fatalf("album = %q, want the release group typed Album", match.Album)
	}
}

func TestLookupAcoustIDNoResultsIsUnmatchedNotError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status": "ok", "results": []}`))
	}))
	defer server.Close()
	orig := acoustidLookupURL
	acoustidLookupURL = server.URL
	t.Cleanup(func() { acoustidLookupURL = orig })

	_, ok, err := lookupAcoustID(context.Background(), server.Client(), "test-key", Fingerprint{DurationSeconds: 320, Value: "AQAA"})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no match")
	}
}

func TestLookupAcoustIDErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status": "error", "error": {"message": "invalid API key"}}`))
	}))
	defer server.Close()
	orig := acoustidLookupURL
	acoustidLookupURL = server.URL
	t.Cleanup(func() { acoustidLookupURL = orig })

	if _, _, err := lookupAcoustID(context.Background(), server.Client(), "bad-key", Fingerprint{DurationSeconds: 320, Value: "AQAA"}); err == nil {
		t.Fatal("expected error for invalid API key")
	}
}

func TestBestReleaseGroupPrefersAlbumTypeAndReturnsID(t *testing.T) {
	groups := []acoustidReleaseGrp{
		{ID: "rg-single", Title: "Homework (Single Edit)", Type: "Single"},
		{ID: "rg-album", Title: "Discovery", Type: "Album"},
	}
	id, title := bestReleaseGroup(groups)
	if title != "Discovery" || id != "rg-album" {
		t.Fatalf("got (%q, %q), want (rg-album, Discovery)", id, title)
	}
}

func TestBestReleaseGroupFallsBackToFirst(t *testing.T) {
	groups := []acoustidReleaseGrp{{ID: "rg-comp", Title: "Compilation Vol. 1", Type: "Compilation"}}
	id, title := bestReleaseGroup(groups)
	if title != "Compilation Vol. 1" || id != "rg-comp" {
		t.Fatalf("got (%q, %q)", id, title)
	}
}

func TestCoverArtArchiveURL(t *testing.T) {
	if got := coverArtArchiveURL("rg-123"); got != "https://coverartarchive.org/release-group/rg-123/front-500" {
		t.Fatalf("got %q", got)
	}
	if got := coverArtArchiveURL("  "); got != "" {
		t.Fatalf("blank id should yield empty URL, got %q", got)
	}
}
