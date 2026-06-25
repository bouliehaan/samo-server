package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveInternetRadioStreamURLReadsPLSPlaylist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/station.pls":
			w.Header().Set("Content-Type", "audio/x-scpls")
			_, _ = w.Write([]byte("[playlist]\nNumberOfEntries=1\nFile1=/live.mp3\nTitle1=Static FM\n"))
		case "/live.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	resolved, err := ResolveInternetRadioStreamURL(context.Background(), server.Client(), server.URL+"/station.pls")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != server.URL+"/live.mp3" {
		t.Fatalf("resolved = %q, want live stream", resolved)
	}
}

func TestResolveInternetRadioStreamURLFollowsNestedM3UPlaylist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first.m3u":
			_, _ = w.Write([]byte("#EXTM3U\n/nested.pls\n"))
		case "/nested.pls":
			_, _ = w.Write([]byte("[playlist]\nFile1=/deep/live.aac\n"))
		case "/deep/live.aac":
			w.Header().Set("Content-Type", "audio/aac")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	resolved, err := ResolveInternetRadioStreamURL(context.Background(), server.Client(), server.URL+"/first.m3u")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != server.URL+"/deep/live.aac" {
		t.Fatalf("resolved = %q, want nested stream", resolved)
	}
}

func TestResolveInternetRadioStreamURLLeavesHLSManifestAlone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:6,\nsegment0001.ts\n"))
	}))
	defer server.Close()

	raw := server.URL + "/live.m3u8"
	resolved, err := ResolveInternetRadioStreamURL(context.Background(), server.Client(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != raw {
		t.Fatalf("resolved = %q, want original HLS manifest", resolved)
	}
}
