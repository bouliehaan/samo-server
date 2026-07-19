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

// TestLookupAcoustIDSendsSplittableMeta guards a production-only bug the mock
// tests above could never catch (they hard-code the response body). AcoustID
// combines multiple `meta` values with "+", which on the wire is a form-encoded
// space. If the value is written as a literal "+", url.Values.Encode() emits
// "%2B" — a literal plus AcoustID does NOT split on, so it returns results with
// NO recordings and every track looks "unmatched" despite a strong fingerprint
// hit. The value must be space-separated so it arrives as two splittable tokens.
func TestLookupAcoustIDSendsSplittableMeta(t *testing.T) {
	var gotMeta string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMeta = r.URL.Query().Get("meta")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status": "ok", "results": []}`))
	}))
	defer server.Close()
	orig := acoustidLookupURL
	acoustidLookupURL = server.URL
	t.Cleanup(func() { acoustidLookupURL = orig })

	if _, _, err := lookupAcoustID(context.Background(), server.Client(), "test-key", Fingerprint{DurationSeconds: 320, Value: "AQAA"}); err != nil {
		t.Fatal(err)
	}
	// The httptest server decodes the query: a correct "recordings+releasegroups"
	// (form-encoded space) arrives as two space-separated values; the buggy
	// literal-plus encoding would arrive as the single token "recordings+releasegroups".
	if gotMeta != "recordings releasegroups" {
		t.Fatalf("meta arrived as %q; want \"recordings releasegroups\" — a literal + encodes to %%2B and makes AcoustID drop every recording", gotMeta)
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

// TestBestReleaseGroupAvoidsCompilations locks in the anti-compilation
// ranking: a classic hit's recording lists dozens of release groups and the
// old "first Album-type" rule happily picked "Ultimate Disco Vol. 7". A clean
// Single must beat a compilation-tainted Album; a derived group is only ever
// a last resort.
func TestBestReleaseGroupAvoidsCompilations(t *testing.T) {
	id, title := bestReleaseGroup([]acoustidReleaseGrp{
		{ID: "rg-comp", Title: "Ultimate Disco", Type: "Album", SecondaryTypes: []string{"Compilation"}},
		{ID: "rg-single", Title: "I Love the Nightlife", Type: "Single"},
	})
	if id != "rg-single" || title != "I Love the Nightlife" {
		t.Fatalf("picked (%q, %q), want the clean single over the compilation album", id, title)
	}

	// Clean Album still beats clean Single.
	id, _ = bestReleaseGroup([]acoustidReleaseGrp{
		{ID: "rg-single", Title: "Single", Type: "Single"},
		{ID: "rg-album", Title: "The Album", Type: "Album"},
	})
	if id != "rg-album" {
		t.Fatalf("picked %q, want the clean album", id)
	}

	// Only derived groups available: better than nothing.
	id, _ = bestReleaseGroup([]acoustidReleaseGrp{
		{ID: "rg-live", Title: "Live at Wembley", Type: "Album", SecondaryTypes: []string{"Live"}},
	})
	if id != "rg-live" {
		t.Fatalf("picked %q, want the derived group as last resort", id)
	}
}
