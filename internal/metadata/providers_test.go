package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenLibraryProviderMapsBookMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("title"); got != "Signal Manual" {
			t.Fatalf("title query = %q, want Signal Manual", got)
		}
		_, _ = w.Write([]byte(`{
			"docs": [{
				"key": "/works/OL1W",
				"title": "Signal Manual",
				"author_name": ["Ada Archive"],
				"first_publish_year": 2026,
				"isbn": ["9780000000001"],
				"cover_i": 123,
				"subject": ["Radio"]
			}]
		}`))
	}))
	defer server.Close()

	provider := NewOpenLibraryProvider(server.Client())
	provider.baseURL = server.URL
	results, err := provider.Search(context.Background(), SearchRequest{
		Kind:  KindAudiobook,
		Title: "Signal Manual",
		Limit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].ExternalIDs.OpenLibraryID != "OL1W" {
		t.Fatalf("open library id = %q, want OL1W", results[0].ExternalIDs.OpenLibraryID)
	}
	if results[0].ExternalIDs.ISBN13 != "9780000000001" {
		t.Fatalf("isbn13 = %q, want 9780000000001", results[0].ExternalIDs.ISBN13)
	}
}

func TestApplePodcastProviderMapsPodcastMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("media"); got != "podcast" {
			t.Fatalf("media query = %q, want podcast", got)
		}
		_, _ = w.Write([]byte(`{
			"results": [{
				"collectionId": 42,
				"collectionName": "Night Signals",
				"artistName": "Ada Archive",
				"feedUrl": "https://example.com/feed.xml",
				"artworkUrl600": "https://example.com/cover.jpg",
				"primaryGenreName": "Fiction",
				"genres": ["Drama"],
				"trackViewUrl": "https://podcasts.apple.com/show/42",
				"releaseDate": "2026-05-22T12:00:00Z"
			}]
		}`))
	}))
	defer server.Close()

	provider := NewApplePodcastProvider(server.Client())
	provider.baseURL = server.URL
	results, err := provider.Search(context.Background(), SearchRequest{
		Kind:  KindPodcast,
		Query: "Night Signals",
		Limit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].ExternalIDs.ITunesID != "42" {
		t.Fatalf("itunes id = %q, want 42", results[0].ExternalIDs.ITunesID)
	}
	if results[0].ExternalIDs.FeedGUID != "" {
		t.Fatalf("feed guid = %q, want provider to leave RSS feed guid alone", results[0].ExternalIDs.FeedGUID)
	}
	if !strings.Contains(strings.Join(results[0].ExternalIDs.URLs, " "), "feed.xml") {
		t.Fatalf("urls = %#v, want feed url", results[0].ExternalIDs.URLs)
	}
}

func TestMusicBrainzProviderMapsRecordingMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "SamoTest/1.0" {
			t.Fatalf("user agent = %q, want SamoTest/1.0", got)
		}
		if !strings.HasPrefix(r.URL.Path, "/recording") {
			t.Fatalf("path = %q, want /recording", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"recordings": [{
				"id": "recording-1",
				"score": "98",
				"title": "Signal One",
				"length": 245000,
				"first-release-date": "2026-05-22",
				"artist-credit": [{
					"name": "The Static",
					"artist": {"id": "artist-1", "name": "The Static", "sort-name": "Static, The"}
				}],
				"releases": [{
					"id": "release-1",
					"title": "Night Broadcasts",
					"release-group": {"id": "rg-1"}
				}]
			}]
		}`))
	}))
	defer server.Close()

	provider := NewMusicBrainzProvider(server.Client(), "SamoTest/1.0")
	provider.baseURL = server.URL
	results, err := provider.Search(context.Background(), SearchRequest{
		Kind:   KindMusic,
		Track:  "Signal One",
		Artist: "The Static",
		Limit:  5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].ExternalIDs.MusicBrainzRecordingID != "recording-1" {
		t.Fatalf("recording id = %q, want recording-1", results[0].ExternalIDs.MusicBrainzRecordingID)
	}
	if results[0].DurationSeconds != 245 {
		t.Fatalf("duration = %d, want 245", results[0].DurationSeconds)
	}
}
