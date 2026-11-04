package lastfm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetRecentTracksReadsScrobblesAndSkipsThePlayingNow(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		asked = append(asked, fmt.Sprintf("%s user=%s from=%s to=%s page=%s", q.Get("method"), q.Get("user"), q.Get("from"), q.Get("to"), q.Get("page")))
		w.Header().Set("Content-Type", "application/json")
		if q.Get("page") == "2" {
			// A lone result comes as an object, not a one-element array.
			fmt.Fprint(w, `{"recenttracks":{"track":{"name":"Low Tide","artist":{"#text":"Aurora Fields"},"album":{"#text":"Low Tide"},"date":{"uts":"1767312000"}},"@attr":{"page":"2","totalPages":"2","total":"3"}}}`)
			return
		}
		fmt.Fprint(w, `{"recenttracks":{"track":[
			{"name":"Now","artist":{"#text":"Someone"},"album":{"#text":""},"@attr":{"nowplaying":"true"}},
			{"name":"Night Drive","artist":{"#text":"Aurora Fields"},"album":{"#text":"Night Drive"},"date":{"uts":"1767398400"},
			 "image":[{"#text":"https://lastfm.example/s.jpg","size":"small"},{"#text":"https://lastfm.example/xl.jpg","size":"extralarge"}]},
			{"name":"Frequencies","artist":{"#text":"Delta Static"},"album":{"#text":"Frequencies"},"date":{"uts":"1767355200"}}
		],"@attr":{"page":"1","totalPages":"2","total":"3"}}}`)
	}))
	defer server.Close()
	client := NewClient("key", "secret", server.Client())
	client.SetAPIBaseURL(server.URL)

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(1, 0, 0)
	page, err := client.GetRecentTracks(context.Background(), "jake_fm", from, to, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalPages != 2 || len(page.Listens) != 2 {
		t.Fatalf("page = %+v", page)
	}
	first := page.Listens[0]
	if first.Track != "Night Drive" || first.Artist != "Aurora Fields" || first.Image != "https://lastfm.example/xl.jpg" ||
		!first.At.Equal(time.Unix(1767398400, 0)) {
		t.Fatalf("first = %+v", first)
	}
	page, err = client.GetRecentTracks(context.Background(), "jake_fm", from, to, 2)
	if err != nil || len(page.Listens) != 1 || page.Listens[0].Track != "Low Tide" {
		t.Fatalf("page 2 = %+v, %v", page, err)
	}
	want := fmt.Sprintf("user.getRecentTracks user=jake_fm from=%d to=%d page=1", from.Unix(), to.Unix())
	if asked[0] != want {
		t.Fatalf("asked %q, want %q", asked[0], want)
	}
}

func TestGetRecentTracksReportsLastFMErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":6,"message":"User not found"}`)
	}))
	defer server.Close()
	client := NewClient("key", "secret", server.Client())
	client.SetAPIBaseURL(server.URL)
	if _, err := client.GetRecentTracks(context.Background(), "nobody", time.Time{}, time.Time{}, 1); err == nil {
		t.Fatal("a Last.fm error was read as an empty year")
	}
}
