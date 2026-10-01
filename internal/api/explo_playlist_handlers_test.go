package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestYouTubeMusicPlaylistID(t *testing.T) {
	for raw, want := range map[string]string{
		"https://music.youtube.com/playlist?list=PLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5":              "PLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5",
		"https://music.youtube.com/playlist?list=PLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5&si=abc":       "PLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5",
		"https://www.youtube.com/watch?v=lwRkYBRCd6U&list=OLAK5uy_kVfFJMDAyz0rmkKqXqGp0Q":         "OLAK5uy_kVfFJMDAyz0rmkKqXqGp0Q",
		"https://music.youtube.com/browse/VLPLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5":                   "PLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5",
		"  https://youtube.com/playlist?list=PLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5  ":                "PLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5",
		"https://music.youtube.com/watch?v=lwRkYBRCd6U":                                           "",
		"https://evil.example/playlist?list=PLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5":                   "",
		"https://music.youtube.com.evil.example/playlist?list=PLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5": "",
		"https://music.youtube.com/playlist?list=LM":                                              "",
		"https://music.youtube.com/playlist?list=../../etc":                                       "",
		"javascript:alert(1)": "",
	} {
		if got := youtubeMusicPlaylistID(raw); got != want {
			t.Errorf("%q → %q, want %q", raw, got, want)
		}
	}
}

// A YouTube Music playlist, end to end through samo's API: the playlist is
// made under its own name with the song the library has, Explo is handed the
// rest in the background, and the import view follows each download.
func TestExploPlaylistImportThroughTheAPI(t *testing.T) {
	const source = "PLjyKKq5ObGwTiTfZlx7ptx4LOu_lhdAY5"
	listing := explo.Playlist{ID: source, Title: "CHET BAKER BALLADS", Tracks: []explo.Song{
		{ID: "youtube-lwRkYBRCd6U", Source: "youtube", Title: "Over the Rainbow", Artist: "Chet Baker", Album: "Chet Is Back!", DurationMS: 209000},
		{ID: "youtube-UIY8stW4xt4", Source: "youtube", Title: "These Foolish Things", Artist: "Chet Baker", Album: "Chet Baker Quartet Vol. 2", DurationMS: 285000},
	}}
	var mu sync.Mutex
	var queued []map[string]string
	playlistsSupported := true
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/samo/status":
			writeJSON(w, 200, map[string]any{"service": "samo-explo", "version": 1, "configured": true, "providers": []string{"youtube"}, "albums": true, "playlists": playlistsSupported})
		case r.URL.Path == "/api/samo/playlists/"+source:
			writeJSON(w, 200, listing)
		case strings.HasPrefix(r.URL.Path, "/api/samo/playlists/"):
			writeJSON(w, 404, map[string]string{"error": "YouTube Music could not open this playlist"})
		case r.Method == "POST" && r.URL.Path == "/api/samo/downloads":
			var input map[string]string
			json.NewDecoder(r.Body).Decode(&input)
			mu.Lock()
			queued = append(queued, input)
			mu.Unlock()
			writeJSON(w, 202, explo.DownloadJob{ID: input["id"], Song: listing.Tracks[1], State: "queued", Provider: "youtube"})
		case r.URL.Path == "/api/samo/downloads/youtube-UIY8stW4xt4":
			writeJSON(w, 200, explo.DownloadJob{ID: "youtube-UIY8stW4xt4", Song: listing.Tracks[1], State: "queued", Message: "Waiting for an available download slot."})
		default:
			t.Errorf("unexpected Explo request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()

	ctx := context.Background()
	db := storagetest.Open(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO music_tracks (id, title, display_artist, duration_seconds) VALUES ('lib-rainbow', 'Over the Rainbow', 'Chet Baker', 209)`); err != nil {
		t.Fatal(err)
	}
	catalogService := catalog.NewService(catalog.Seed{})
	reload := func(ctx context.Context) error {
		seed, err := catalogstore.LoadSeedFromDB(ctx, db)
		if err != nil {
			return err
		}
		catalogService.Replace(seed)
		return nil
	}
	if err := reload(ctx); err != nil {
		t.Fatal(err)
	}
	playlistService := playlists.New(db)
	service := explo.NewService(explo.ServiceOptions{DB: db, Dirs: []string{"/music/explo"}, AcoustIDAPIKey: "key", FpcalcPath: "/fake/fpcalc",
		Playlists: playlistService, ReloadCatalog: reload})
	handler := NewServer(ServerOptions{DB: db, Catalog: catalogService, Playlists: playlistService, ReloadCatalog: reload,
		Explo: service, ExploRemote: explo.NewRemote(upstream.URL, "secret")})
	call := func(method, path, body string) *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(method, path, strings.NewReader(body)))
		return res
	}

	var status exploDiscoveryStatus
	json.Unmarshal(call("GET", "/api/v1/explo/discovery/status", "").Body.Bytes(), &status)
	if !status.Playlists {
		t.Fatal("playlist import not reported available")
	}
	if res := call("POST", "/api/v1/music/playlists/explo-import", `{"url":"https://music.youtube.com/watch?v=lwRkYBRCd6U"}`); res.Code != 400 {
		t.Fatalf("a link without a playlist: %d", res.Code)
	}
	if res := call("POST", "/api/v1/music/playlists/explo-import", `{"url":"https://music.youtube.com/playlist?list=PLprivate_private"}`); res.Code != 404 || !strings.Contains(res.Body.String(), "public or unlisted") {
		t.Fatalf("an unreadable playlist: %d %s", res.Code, res.Body.String())
	}

	res := call("POST", "/api/v1/music/playlists/explo-import", `{"url":"https://music.youtube.com/playlist?list=`+source+`"}`)
	if res.Code != http.StatusAccepted {
		t.Fatalf("import %d %s", res.Code, res.Body.String())
	}
	var imported exploPlaylistImportResponse
	if err := json.Unmarshal(res.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Playlist.Name != "CHET BAKER BALLADS" || len(imported.Playlist.TrackIDs) != 1 || imported.Playlist.TrackIDs[0] != "lib-rainbow" ||
		len(imported.Import.Tracks) != 2 || imported.Import.Tracks[0].State != explo.RequestInLibrary {
		t.Fatalf("imported %s", res.Body.String())
	}
	// The playlist is in the projection at once, with its library song.
	if res := call("GET", "/api/v1/music/playlists/"+imported.Playlist.ID+"/tracks", ""); res.Code != 200 || !strings.Contains(res.Body.String(), `"id":"lib-rainbow"`) {
		t.Fatalf("playlist tracks %d %s", res.Code, res.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(queued)
		mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	if len(queued) != 1 || queued[0]["id"] != "youtube-UIY8stW4xt4" || queued[0]["playlist"] != source {
		t.Fatalf("Explo was asked for %v; want only the missing song, from its playlist", queued)
	}
	mu.Unlock()

	var view explo.PlaylistImport
	res = call("GET", "/api/v1/music/playlists/"+imported.Playlist.ID+"/import", "")
	json.Unmarshal(res.Body.Bytes(), &view)
	if res.Code != 200 || view.Title != "CHET BAKER BALLADS" || view.Tracks[1].State != explo.RequestDownloading || !strings.Contains(view.Tracks[1].Message, "download slot") {
		t.Fatalf("import view %d %s", res.Code, res.Body.String())
	}
	if res := call("GET", "/api/v1/music/playlists/nope/import", ""); res.Code != 404 {
		t.Fatalf("an unknown playlist: %d", res.Code)
	}

	playlistsSupported = false
	if res := call("POST", "/api/v1/music/playlists/explo-import", `{"url":"https://music.youtube.com/playlist?list=`+source+`"}`); res.Code != 503 || !strings.Contains(res.Body.String(), "Update samo-explo") {
		t.Fatalf("an Explo too old to read playlists: %d %s", res.Code, res.Body.String())
	}
}
