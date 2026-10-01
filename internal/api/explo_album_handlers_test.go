package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

const (
	okComputer   = "b1392450-e666-3926-a536-22c65f834433"
	airbag       = "4a7fea2e-545b-4c63-bc9a-9943cc3a29d7"
	paranoid     = "2c1a2f3e-0d6c-4a9e-9b0e-6f3c1c2d4e55"
	subterranean = "6b0f1a1e-3f2d-4c1b-8a7e-1d2c3b4a5f66"
)

func okComputerTrack(id, title string, number int) explo.Song {
	return explo.Song{ID: id, Title: title, Artist: "Radiohead", Album: "OK Computer", DurationMS: 280000, AlbumID: okComputer,
		AlbumTrack: &explo.AlbumTrack{AlbumID: okComputer, AlbumTitle: "OK Computer", AlbumArtist: "Radiohead",
			ReleaseID: "jp-1997", Number: number, TrackTotal: 12, Disc: 1, DiscTotal: 1, Year: 1997}}
}

// The whole album, end to end through samo's API: found, opened, and asked
// for — Explo is asked only for the track samo has nowhere, the track already
// in the library on this album is recorded as there, and the song already
// requested on its own joins the album instead of downloading twice.
func TestExploAlbumIsRequestedTrackByTrack(t *testing.T) {
	var mu sync.Mutex
	var queued []map[string]string
	album := explo.Album{ID: okComputer, Title: "OK Computer", Artist: "Radiohead", Type: "Album", Year: 1997, ReleaseID: "jp-1997",
		Tracks: []explo.Song{okComputerTrack(airbag, "Airbag", 1), okComputerTrack(paranoid, "Paranoid Android", 2), okComputerTrack(subterranean, "Subterranean Homesick Alien", 3)}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/samo/status":
			writeJSON(w, 200, map[string]any{"service": "samo-explo", "version": 1, "configured": true, "providers": []string{"youtube"}, "albums": true})
		case r.URL.Path == "/api/samo/albums" && r.URL.Query().Get("q") == "ok computer":
			writeJSON(w, 200, explo.AlbumResults{Albums: []explo.Album{{ID: okComputer, Title: "OK Computer", Artist: "Radiohead", Type: "Album", Year: 1997}}})
		case r.URL.Path == "/api/samo/albums/"+okComputer:
			writeJSON(w, 200, album)
		case r.Method == "POST" && r.URL.Path == "/api/samo/downloads":
			var input map[string]string
			json.NewDecoder(r.Body).Decode(&input)
			mu.Lock()
			queued = append(queued, input)
			mu.Unlock()
			writeJSON(w, 202, explo.DownloadJob{ID: input["id"], Song: album.Tracks[0], State: "queued", Provider: "youtube"})
		case r.URL.Path == "/api/samo/downloads/"+airbag:
			writeJSON(w, 200, explo.DownloadJob{ID: airbag, Song: album.Tracks[0], State: "downloading", Provider: "youtube", Message: "YouTube (yt-dlp): downloading audio…"})
		default:
			t.Errorf("unexpected Explo request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()

	ctx := context.Background()
	db := storagetest.Open(t)
	service := explo.NewService(explo.ServiceOptions{DB: db, Dirs: []string{"/music/explo"}, AcoustIDAPIKey: "key", FpcalcPath: "/fake/fpcalc"})
	for _, stmt := range []string{
		`INSERT INTO music_albums (id, title, track_count) VALUES ('album-okc', 'OK Computer', 1)`,
		`INSERT INTO music_tracks (id, title, display_artist, album_id, album_title, duration_seconds, external_ids_json)
		 VALUES ('library-paranoid', 'Paranoid Android', 'Radiohead', 'album-okc', 'OK Computer', 383, '{"musicBrainzRecordingId":"` + paranoid + `"}')`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	// Asked for on its own last week, and still being identified.
	solo := explo.DownloadJob{ID: subterranean, State: "staged", Song: explo.Song{ID: subterranean, Title: "Subterranean Homesick Alien", Artist: "Radiohead", Album: "OK Computer OKNOTOK"}}
	if err := service.RecordRequest(ctx, solo, "user-owner"); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{DB: db, Explo: service, ExploRemote: explo.NewRemote(upstream.URL, "secret")})
	call := func(method, path, body string) *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(method, path, strings.NewReader(body)))
		return res
	}

	var status exploDiscoveryStatus
	json.Unmarshal(call("GET", "/api/v1/explo/discovery/status", "").Body.Bytes(), &status)
	if !status.Albums {
		t.Fatal("album search not reported available")
	}
	res := call("GET", "/api/v1/explo/albums?q=ok+computer", "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"id":"`+okComputer+`"`) {
		t.Fatalf("album search %d %s", res.Code, res.Body.String())
	}
	res = call("GET", "/api/v1/explo/albums/"+okComputer, "")
	var opened exploAlbumView
	json.Unmarshal(res.Body.Bytes(), &opened)
	if res.Code != 200 || len(opened.Tracks) != 3 || opened.Tracks[0].Job != nil || opened.Tracks[2].Job == nil {
		t.Fatalf("album lookup %d %s", res.Code, res.Body.String())
	}

	res = call("POST", "/api/v1/explo/albums/"+okComputer+"/downloads", `{"provider":"youtube"}`)
	if res.Code != http.StatusAccepted {
		t.Fatalf("album download %d %s", res.Code, res.Body.String())
	}
	if len(queued) != 1 || queued[0]["id"] != airbag || queued[0]["album"] != okComputer || queued[0]["provider"] != "youtube" {
		t.Fatalf("Explo was asked for %v; want only Airbag, as the album's track", queued)
	}
	var view exploAlbumDownloadView
	json.Unmarshal(res.Body.Bytes(), &view)
	states := map[string]string{}
	for _, track := range view.Tracks {
		state := track.Job.State
		if track.Job.Library != nil {
			state += "/" + track.Job.Library.State
		}
		states[track.Title] = state
	}
	if view.Title != "OK Computer" || len(view.Tracks) != 3 || view.Tracks[0].Title != "Airbag" ||
		states["Airbag"] != "downloading" || states["Paranoid Android"] != "staged/in-library" || states["Subterranean Homesick Alien"] != "staged/identifying" {
		t.Fatalf("album request %s", res.Body.String())
	}
	if request, _, _ := service.Request(ctx, subterranean); request.AlbumID != okComputer {
		t.Fatalf("the song requested on its own did not join the album: %+v", request)
	}
	if request, _, _ := service.Request(ctx, paranoid); request.LibraryTrackID != "library-paranoid" {
		t.Fatalf("library copy not recorded: %+v", request)
	}

	// Following it: Explo is asked about the track it is downloading.
	res = call("GET", "/api/v1/explo/albums/"+okComputer+"/download", "")
	json.Unmarshal(res.Body.Bytes(), &view)
	if res.Code != 200 || view.Tracks[0].Job.State != "downloading" || !strings.Contains(view.Tracks[0].Job.Message, "downloading audio") || view.Tracks[0].Job.File != "" {
		t.Fatalf("album progress %d %s", res.Code, res.Body.String())
	}
	if res := call("GET", "/api/v1/explo/albums/12345678-1234-1234-1234-123456789012/download", ""); res.Code != 404 {
		t.Fatalf("an album never requested: %d", res.Code)
	}
	if res := call("POST", "/api/v1/explo/albums/not-an-id/downloads", `{}`); res.Code != 400 {
		t.Fatalf("invalid album id: %d", res.Code)
	}

	// Asking again resumes: nothing still on its way is asked for twice.
	call("POST", "/api/v1/explo/albums/"+okComputer+"/downloads", ``)
	if len(queued) != 1 {
		t.Fatalf("asking again re-queued %v", queued)
	}
}

func TestExploAlbumRoutesRequireBearer(t *testing.T) {
	s := discoveryTestServer(t, true, nil, "account-secret")
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/explo/albums?q=album"},
		{"GET", "/api/v1/explo/albums/" + okComputer},
		{"POST", "/api/v1/explo/albums/" + okComputer + "/downloads"},
		{"GET", "/api/v1/explo/albums/" + okComputer + "/download"},
		{"GET", "/api/v1/explo/art/" + okComputer},
	} {
		response := httptest.NewRecorder()
		s.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != 401 {
			t.Errorf("%s %s: %d", route.method, route.path, response.Code)
		}
	}
}

// A page of results asks the archive once per cover, remembers which albums
// have none, and does not remember an outage as either.
func TestExploArtIsFetchedOnceAndMissingArtIsRemembered(t *testing.T) {
	var fetches atomic.Int32
	var down atomic.Bool
	archive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		switch {
		case down.Load():
			w.WriteHeader(503)
		case strings.Contains(r.URL.Path, okComputer):
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write([]byte("\xff\xd8 a cover"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer archive.Close()
	previous := exploArtURL
	exploArtURL = func(id string) string { return archive.URL + "/release-group/" + id + "/front-250" }
	t.Cleanup(func() { exploArtURL = previous })

	handler := discoveryTestServer(t, true, nil, "")
	get := func(id string) *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest("GET", "/api/v1/explo/art/"+id, nil))
		return res
	}
	for i := 0; i < 3; i++ {
		res := get(okComputer)
		if res.Code != 200 || res.Header().Get("Content-Type") != "image/jpeg" || res.Body.String() != "\xff\xd8 a cover" {
			t.Fatalf("cover %d %q", res.Code, res.Body.String())
		}
	}
	none := "12345678-1234-1234-1234-123456789012"
	for i := 0; i < 2; i++ {
		if res := get(none); res.Code != 404 {
			t.Fatalf("missing cover %d", res.Code)
		}
	}
	if fetches.Load() != 2 {
		t.Fatalf("archive asked %d times; want once per album", fetches.Load())
	}
	if res := get("not-a-release-group"); res.Code != 400 {
		t.Fatalf("non-id accepted: %d", res.Code)
	}

	down.Store(true)
	outage := "22345678-1234-1234-1234-123456789012"
	if res := get(outage); res.Code != 502 || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("outage %d %q", res.Code, res.Header().Get("Cache-Control"))
	}
	down.Store(false)
	before := fetches.Load()
	get(outage)
	if fetches.Load() != before+1 {
		t.Fatal("an outage was remembered as no cover")
	}
}

// An album only Deezer lists goes through the same routes as any other.
func TestExploDeezerAlbumIsRequested(t *testing.T) {
	const albumID, trackID = "deezer-546260002", "deezer-2657539542"
	track := explo.Song{ID: trackID, Source: "deezer", Title: "puer aeternus", Artist: "Quangou", Album: "Puer Aeternus", DurationMS: 102000, AlbumID: albumID,
		AlbumTrack: &explo.AlbumTrack{AlbumID: albumID, AlbumTitle: "Puer Aeternus", AlbumArtist: "Quangou", Number: 1, TrackTotal: 10, Disc: 1, DiscTotal: 1, Year: 2024}}
	var queued map[string]string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/samo/status":
			writeJSON(w, 200, map[string]any{"service": "samo-explo", "version": 1, "configured": true, "providers": []string{"youtube"}, "albums": true})
		case r.URL.Path == "/api/samo/albums/"+albumID:
			writeJSON(w, 200, explo.Album{ID: albumID, Source: "deezer", Title: "Puer Aeternus", Artist: "Quangou", Type: "Album", Year: 2024, Tracks: []explo.Song{track}})
		case r.Method == "POST" && r.URL.Path == "/api/samo/downloads":
			json.NewDecoder(r.Body).Decode(&queued)
			writeJSON(w, 202, explo.DownloadJob{ID: trackID, Song: track, State: "queued", Provider: "youtube"})
		case r.URL.Path == "/api/samo/downloads/"+trackID:
			writeJSON(w, 200, explo.DownloadJob{ID: trackID, Song: track, State: "downloading", Provider: "youtube"})
		default:
			t.Errorf("unexpected Explo request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	db := storagetest.Open(t)
	service := explo.NewService(explo.ServiceOptions{DB: db, Dirs: []string{"/music/explo"}, AcoustIDAPIKey: "key", FpcalcPath: "/fake/fpcalc"})
	handler := NewServer(ServerOptions{DB: db, Explo: service, ExploRemote: explo.NewRemote(upstream.URL, "secret")})
	call := func(method, path, body string) *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(method, path, strings.NewReader(body)))
		return res
	}
	if res := call("GET", "/api/v1/explo/albums/"+albumID, ""); res.Code != 200 || !strings.Contains(res.Body.String(), `"id":"`+trackID+`"`) {
		t.Fatalf("lookup %d %s", res.Code, res.Body.String())
	}
	res := call("POST", "/api/v1/explo/albums/"+albumID+"/downloads", `{}`)
	if res.Code != http.StatusAccepted || queued["id"] != trackID || queued["album"] != albumID {
		t.Fatalf("download %d %s; Explo asked %v", res.Code, res.Body.String(), queued)
	}
	res = call("GET", "/api/v1/explo/albums/"+albumID+"/download", "")
	var view exploAlbumDownloadView
	json.Unmarshal(res.Body.Bytes(), &view)
	if res.Code != 200 || view.Title != "Puer Aeternus" || len(view.Tracks) != 1 || view.Tracks[0].Job.State != "downloading" {
		t.Fatalf("album progress %d %s", res.Code, res.Body.String())
	}
	if res := call("GET", "/api/v1/explo/albums/deezer-12x/download", ""); res.Code != 400 {
		t.Fatalf("malformed Deezer id: %d", res.Code)
	}
}

func TestExploArtURLFollowsTheAlbumsCatalog(t *testing.T) {
	if got := exploArtURL("deezer-546260002"); got != "https://api.deezer.com/album/546260002/image?size=big" {
		t.Fatalf("Deezer cover at %q", got)
	}
	if got := exploArtURL(okComputer); got != "https://coverartarchive.org/release-group/"+okComputer+"/front-250" {
		t.Fatalf("release group cover at %q", got)
	}
}
