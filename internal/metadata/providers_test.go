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

func TestAudibleProviderMapsASINLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/books/"):
			if r.URL.Query().Get("region") != "us" {
				t.Fatalf("region = %q, want us", r.URL.Query().Get("region"))
			}
			_, _ = w.Write([]byte(`{
				"asin": "B002V5GLQ4",
				"title": "The Well of Ascension",
				"subtitle": "Mistborn, Book 2",
				"description": "Book two in the Mistborn saga.",
				"image": "https://example.com/cover.jpg",
				"authors": [{"asin": "B001IGFHW6", "name": "Brandon Sanderson"}],
				"narrators": [{"name": "Michael Kramer"}],
				"genres": [{"name": "Fantasy", "type": "genre"}],
				"publisherName": "Macmillan Audio",
				"releaseDate": "2009-03-09T00:00:00.000Z",
				"runtimeLengthMin": 1736,
				"isbn": "9781427206381",
				"seriesPrimary": {"asin": "B006K1P698", "name": "The Mistborn Saga", "position": "2"}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewAudibleProvider(server.Client())
	provider.audnexusURL = server.URL
	results, err := provider.Search(context.Background(), SearchRequest{
		Kind:        KindAudiobook,
		AudibleASIN: "B002V5GLQ4",
		Limit:       5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].ExternalIDs.AudibleASIN != "B002V5GLQ4" {
		t.Fatalf("audible asin = %q, want B002V5GLQ4", results[0].ExternalIDs.AudibleASIN)
	}
	if results[0].Cover == nil || results[0].Cover.URL != "https://example.com/cover.jpg" {
		t.Fatalf("cover = %#v, want example cover url", results[0].Cover)
	}
	if results[0].DurationSeconds != 1736*60 {
		t.Fatalf("duration = %d, want %d", results[0].DurationSeconds, 1736*60)
	}
}

func TestAudibleProviderSearchesCatalogThenLoadsBooks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/1.0/catalog/products"):
			if got := r.URL.Query().Get("title"); got != "Signal Manual" {
				t.Fatalf("title query = %q, want Signal Manual", got)
			}
			_, _ = w.Write([]byte(`{"products":[{"asin":"B000TEST01"}]}`))
		case strings.HasPrefix(r.URL.Path, "/books/"):
			_, _ = w.Write([]byte(`{
				"asin": "B000TEST01",
				"title": "Signal Manual",
				"authors": [{"name": "Ada Archive"}],
				"image": "https://example.com/signal.jpg",
				"runtimeLengthMin": 120
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewAudibleProvider(server.Client())
	provider.audnexusURL = server.URL
	provider.catalogProductsURL = server.URL + "/1.0/catalog/products"
	results, err := provider.Search(context.Background(), SearchRequest{
		Kind:   KindAudiobook,
		Title:  "Signal Manual",
		Author: "Ada Archive",
		Limit:  5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Title != "Signal Manual" {
		t.Fatalf("title = %q, want Signal Manual", results[0].Title)
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
