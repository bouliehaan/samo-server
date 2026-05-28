package playlists

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestExtractYouTubeInitialDataFromScriptTag(t *testing.T) {
	html := `<html><script id="ytInitialData" type="application/json">{"responseContext":{},"contents":{"x":1}}</script></html>`
	got := extractYouTubeInitialData(html)
	if !strings.Contains(got, `"contents"`) {
		t.Fatalf("extractYouTubeInitialData() = %q", got)
	}
}

func TestParseYouTubeImportPlaylistVideoRenderer(t *testing.T) {
	payload := `{
	  "contents": {
	    "twoColumnBrowseResultsRenderer": {
	      "tabs": [{
	        "tabRenderer": {
	          "content": {
	            "sectionListRenderer": {
	              "contents": [{
	                "itemSectionRenderer": {
	                  "contents": [{
	                    "playlistVideoListRenderer": {
	                      "contents": [{
	                        "playlistVideoRenderer": {
	                          "title": {"runs": [{"text": "First Song"}]},
	                          "shortBylineText": {"runs": [{"text": "Artist One"}]},
	                          "lengthText": {"simpleText": "3:45"}
	                        }
	                      }]
	                    }
	                  }]
	                }
	              }]
	            }
	          }
	        }
	      }]
	    }
	  }
	}`
	items, err := parseYouTubeImport(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Title != "First Song" {
		t.Fatalf("title = %q", items[0].Title)
	}
	if items[0].Artist != "Artist One" {
		t.Fatalf("artist = %q", items[0].Artist)
	}
	if items[0].DurationSeconds != 225 {
		t.Fatalf("duration = %d, want 225", items[0].DurationSeconds)
	}
}

func TestNormalizeYouTubePlaylistFetchURL(t *testing.T) {
	got := normalizeYouTubePlaylistFetchURL("PLabc123")
	want := "https://www.youtube.com/playlist?list=PLabc123"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFetchYouTubePlaylistContentIntegration(t *testing.T) {
	if os.Getenv("SAMO_LIVE_YOUTUBE_TEST") == "" {
		t.Skip("set SAMO_LIVE_YOUTUBE_TEST=1 to run live youtube import test")
	}
	content, err := fetchYouTubePlaylistContent(context.Background(), "https://music.youtube.com/playlist?list=PLrAXtmErZgOeiKm4sgNOknGvNjby9efdf")
	if err != nil {
		t.Fatal(err)
	}
	items, err := parseYouTubeImport(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("expected playlist tracks")
	}
}
