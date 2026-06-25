package artistimages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func TestDeezerArtistPictureURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/search/artist") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [{
				"name": "Kanye West",
				"picture_xl": "https://cdn-images.dzcdn.net/images/artist/test/1000x1000.jpg"
			}]
		}`))
	}))
	defer server.Close()

	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "api.deezer.com") {
			req.URL.Scheme = "http"
			req.URL.Host = strings.TrimPrefix(strings.TrimPrefix(server.URL, "https://"), "http://")
			req.URL.Path = "/search/artist"
		}
		return http.DefaultTransport.RoundTrip(req)
	})}

	picture, err := deezerArtistPictureURL(context.Background(), client, "Kanye West", "Ye")
	if err != nil {
		t.Fatal(err)
	}
	if picture == "" {
		t.Fatal("expected deezer picture url")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestLookupArtistNamesDedupesAndSplitsCollaborators(t *testing.T) {
	names := lookupArtistNames(catalog.MusicArtist{Name: "Action Bronson & Statik Selektah"})
	if len(names) < 2 {
		t.Fatalf("names = %#v, want split collaborator variants", names)
	}
}
