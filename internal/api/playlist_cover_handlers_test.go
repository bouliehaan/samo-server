package api

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestPlaylistCoverUsesCachedSourcesAndRendersFourQuadrants(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	db := storagetest.Open(t)
	coverDir := t.TempDir()
	store, err := covers.New(db, covers.Options{CoverDir: coverDir})
	if err != nil {
		t.Fatal(err)
	}
	palette := []color.RGBA{{R: 255, A: 255}, {G: 255, A: 255}, {B: 255, A: 255}, {R: 255, G: 255, A: 255}}
	tracks := make([]catalog.MusicTrack, 4)
	ids := []string{"t1", "t2", "t3", "t4"}
	for i, c := range palette {
		im := image.NewRGBA(image.Rect(0, 0, 40, 60))
		for y := 0; y < 60; y++ {
			for x := 0; x < 40; x++ {
				im.Set(x, y, c)
			}
		}
		var data bytes.Buffer
		if err := png.Encode(&data, im); err != nil {
			t.Fatal(err)
		}
		stored, err := store.StoreFromUpload(context.Background(), ids[i], "image/png", &data)
		if err != nil {
			t.Fatal(err)
		}
		// Real-world failure: old path and provenance URL on a cover whose ID
		// resolves to healthy local bytes. Neither old location should be opened.
		tracks[i] = catalog.MusicTrack{ID: ids[i], Images: []catalog.Image{{
			ID: stored.ID, Path: filepath.Join(coverDir, "gone.png"), URL: "https://unreachable.invalid/cover.png",
		}}}
	}
	cat := catalog.NewService(catalog.Seed{
		MusicTracks:    tracks,
		MusicPlaylists: []catalog.MusicPlaylist{{ID: "pl1", Name: "Grid", TrackIDs: ids}},
	})
	handler := NewServer(ServerOptions{Catalog: cat, Covers: store, Files: files.New(db, coverDir)})
	rec, body := fetchImage(t, handler, "/api/v1/music/playlists/pl1/cover?artwork=2")
	if got := rec.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("cache policy: %s", got)
	}
	decoded, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 600 || decoded.Bounds().Dy() != 600 {
		t.Fatalf("expected 600x600 grid, got %v", decoded.Bounds())
	}
	for i, want := range palette {
		got := color.RGBAModel.Convert(decoded.At(150+(i%2)*300, 150+(i/2)*300)).(color.RGBA)
		near := func(a, b uint8) bool { d := int(a) - int(b); return d >= -12 && d <= 12 }
		if !near(got.R, want.R) || !near(got.G, want.G) || !near(got.B, want.B) {
			t.Errorf("quadrant %d = %v, want %v", i, got, want)
		}
	}
	// A compositor failure must still serve the repaired first cover, not the
	// original dead path. Single/custom art needs the same repair.
	brokenStore, err := covers.New(db, covers.Options{CoverDir: coverDir, FFmpegPath: filepath.Join(coverDir, "missing-ffmpeg")})
	if err != nil {
		t.Fatal(err)
	}
	fallbackHandler := NewServer(ServerOptions{Catalog: cat, Covers: brokenStore, Files: files.New(db, coverDir)})
	cat.UpsertMusicPlaylist(catalog.MusicPlaylist{ID: "fallback", Name: "Fallback", TrackIDs: ids})
	cat.UpsertMusicPlaylist(catalog.MusicPlaylist{ID: "single", Name: "Single", TrackIDs: ids[:1]})
	for _, id := range []string{"fallback", "single"} {
		_, data := fetchImage(t, fallbackHandler, "/api/v1/music/playlists/"+id+"/cover")
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width != 40 || config.Height != 60 {
			t.Fatalf("%s fallback: %v, %v", id, config, err)
		}
	}

}

func TestPlaylistCoverDownloadsRemoteSourcesBeforeCompositing(t *testing.T) {
	db := storagetest.Open(t)
	store, err := covers.New(db, covers.Options{CoverDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	store.SetRemoteOptions(covers.RemoteOptions{AllowPrivateHosts: true})
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, image.NewRGBA(image.Rect(0, 0, 10, 10)))
	}))
	defer upstream.Close()
	server := &Server{covers: store, catalog: catalog.NewService(catalog.Seed{})}
	images := []catalog.Image{{URL: upstream.URL + "/art.png"}, {URL: upstream.URL + "/art.png"}}
	_, paths := server.playlistCoverCompositeSources(httptest.NewRequest("GET", "/", nil), images)
	if len(paths) != 2 {
		t.Fatalf("sources: %v", paths)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("not a local source: %s: %v", path, err)
		}
	}
	if hits != 1 {
		t.Fatalf("downloaded repeated source %d times", hits)
	}
}
