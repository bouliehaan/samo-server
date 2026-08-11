package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/users"
)

func samoRadioTestServer() *Server {
	return &Server{
		listenAddr: ":6969",
		catalog: catalog.NewService(catalog.Seed{
			MusicTracks: []catalog.MusicTrack{{
				ID:              "track_1",
				Title:           "Peggy Gou",
				DisplayArtist:   "Peggy Gou",
				AlbumID:         "album_1",
				DurationSeconds: 214,
			}},
		}),
	}
}

// The two base URLs must not be mixed up. The DEVICE fetches audio and lives
// next to the server, so stream URLs are built on the loopback base; the CLIENT
// fetches artwork and may be anywhere, so those are built on the request host.
// Getting this backwards sends the device's audio out through the public tunnel
// and back.
func TestResolveSamoRadioItemSplitsStreamAndArtworkHosts(t *testing.T) {
	server := samoRadioTestServer()
	request := httptest.NewRequest("POST", "https://samo.example.com/api/v1/samo-radio/devices/d1/play", nil)
	request.Host = "samo.example.com"

	item, err := server.resolveSamoRadioItem(
		context.Background(),
		users.Principal{User: users.User{ID: "user_1"}},
		"http://127.0.0.1:6969",
		request,
		"track",
		"track_1",
	)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if item.StreamURL != "http://127.0.0.1:6969/api/v1/music/tracks/track_1/stream" {
		t.Fatalf("stream URL must target the device's local base, got %q", item.StreamURL)
	}
	if !strings.HasPrefix(item.ArtworkURL, "https://samo.example.com/") {
		t.Fatalf("artwork URL must target the requesting client's host, got %q", item.ArtworkURL)
	}
	if item.Title != "Peggy Gou" || item.Subtitle != "Peggy Gou" {
		t.Fatalf("unexpected metadata: %+v", item)
	}
	if item.DurationSeconds != 214 {
		t.Fatalf("expected the catalog duration, got %v", item.DurationSeconds)
	}
	if item.Live {
		t.Fatal("a track is not live")
	}
	if item.Ref != "track:track_1" {
		t.Fatalf("unexpected ref %q", item.Ref)
	}
}

// A live source must be flagged: the device skips seeking on it and retries a
// dropped connection instead of treating it as the end of the item.
func TestResolveSamoRadioItemMarksRadioLive(t *testing.T) {
	server := samoRadioTestServer()
	request := httptest.NewRequest("POST", "http://samo.local/x", nil)

	item, err := server.resolveSamoRadioItem(
		context.Background(),
		users.Principal{},
		"http://127.0.0.1:6969",
		request,
		"radio",
		"station_9",
	)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !item.Live {
		t.Fatal("a rotation station is live")
	}
	if item.StreamURL != "http://127.0.0.1:6969/radio/station_9/stream" {
		t.Fatalf("unexpected stream URL %q", item.StreamURL)
	}
}

func TestResolveSamoRadioItemRejectsUnknownType(t *testing.T) {
	server := samoRadioTestServer()
	request := httptest.NewRequest("POST", "http://samo.local/x", nil)
	if _, err := server.resolveSamoRadioItem(context.Background(), users.Principal{}, "http://127.0.0.1:6969", request, "url", "https://evil.example.com/x.mp3"); err == nil {
		t.Fatal("clients must not be able to hand the device an arbitrary URL")
	}
}

func TestResolveSamoRadioItemsSkipsBlankIDs(t *testing.T) {
	server := samoRadioTestServer()
	request := httptest.NewRequest("POST", "http://samo.local/x", nil)
	items, err := server.resolveSamoRadioItems(
		request,
		users.Principal{},
		"http://127.0.0.1:6969",
		[]playItemRef{{Type: "track", ID: "  "}, {Type: "track", ID: "track_1"}},
	)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected the blank reference to be dropped, got %d", len(items))
	}
}

// The device is told where Samo is from the listen address, never from the
// request — a phone pairing over the tunnel must not point the device at it.
func TestLoopbackBaseURL(t *testing.T) {
	cases := map[string]string{
		":6969":          "http://127.0.0.1:6969",
		"0.0.0.0:6969":   "http://127.0.0.1:6969",
		"127.0.0.1:8080": "http://127.0.0.1:8080",
		"":               "http://127.0.0.1:6969",
	}
	for addr, want := range cases {
		server := &Server{listenAddr: addr}
		if got := server.loopbackBaseURL(); got != want {
			t.Fatalf("listenAddr %q: got %q want %q", addr, got, want)
		}
	}
}

// With no service wired up the picker must see an empty list, not an error:
// clients ask for this every time the output sheet opens.
func TestListSamoRadioDevicesWithoutServiceReturnsEmpty(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	server.listSamoRadioDevices(recorder, httptest.NewRequest("GET", "/api/v1/samo-radio/devices", nil))
	if recorder.Code != 200 {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"total":0`) {
		t.Fatalf("expected an empty list, got %s", recorder.Body.String())
	}
}
