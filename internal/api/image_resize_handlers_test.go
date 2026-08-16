package api

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/images"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// imageRouteFixture wires the minimum needed to serve one catalog image from a
// real library root, with thumbnailing enabled.
func imageRouteFixture(t *testing.T, coverW, coverH int) (http.Handler, int64) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	libraryDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}

	source := image.NewRGBA(image.Rect(0, 0, coverW, coverH))
	for y := range coverH {
		for x := range coverW {
			source.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: 255})
		}
	}
	coverPath := filepath.Join(libraryDir, "cover.jpg")
	file, err := os.Create(coverPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(file, source, nil); err != nil {
		t.Fatal(err)
	}
	file.Close()

	info, err := os.Stat(coverPath)
	if err != nil {
		t.Fatal(err)
	}

	db := storagetest.Open(t)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path)
		VALUES ('library-1', 'Music', 'music', '', ?)`, libraryDir); err != nil {
		t.Fatal(err)
	}

	thumbDir := filepath.Join(root, "thumbs")
	thumbnails, err := images.New(thumbDir)
	if err != nil {
		t.Fatal(err)
	}

	catalogService := catalog.NewService(catalog.Seed{
		MusicAlbums: []catalog.MusicAlbum{{
			ID:     "album-1",
			Title:  "Test Album",
			Images: []catalog.Image{{ID: "image_cover1", Path: coverPath}},
		}},
	})

	handler := NewServer(ServerOptions{
		Catalog:    catalogService,
		Files:      files.New(db, thumbDir),
		Thumbnails: thumbnails,
	})
	return handler, info.Size()
}

// fetchImage returns the response plus its body as a stable byte slice.
// rec.Body is a bytes.Buffer, so decoding straight from it consumes the bytes
// and any later length check reads whatever the decoder left behind.
func fetchImage(t *testing.T, handler http.Handler, url string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, body = %s", url, rec.Code, rec.Body.String())
	}
	return rec, rec.Body.Bytes()
}

func TestImageRouteServesRequestedWidth(t *testing.T) {
	handler, originalSize := imageRouteFixture(t, 1400, 1400)

	rec, body := fetchImage(t, handler, "/api/v1/media/images/image_cover1/image?width=128")

	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if config.Width != 128 || config.Height != 128 {
		t.Fatalf("served %dx%d, want 128x128", config.Width, config.Height)
	}
	if int64(len(body)) >= originalSize {
		t.Fatalf("variant is %d bytes, not smaller than the %d byte original",
			len(body), originalSize)
	}
	// The variant is as immutable as the original — same bytes for the same
	// URL forever — so it must keep the long-lived cache headers.
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestImageRouteWithoutWidthServesOriginal(t *testing.T) {
	handler, originalSize := imageRouteFixture(t, 1400, 1400)

	_, body := fetchImage(t, handler, "/api/v1/media/images/image_cover1/image")

	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if config.Width != 1400 || config.Height != 1400 {
		t.Fatalf("served %dx%d, want the untouched 1400x1400 original",
			config.Width, config.Height)
	}
	if int64(len(body)) != originalSize {
		t.Fatalf("served %d bytes, want the %d byte original", len(body), originalSize)
	}
}

func TestImageRouteSnapsWidthToLadder(t *testing.T) {
	handler, _ := imageRouteFixture(t, 1400, 1400)

	// 200 is not a rung; it must round up to 256 rather than render a
	// bespoke variant for every width a client happens to ask for.
	_, body := fetchImage(t, handler, "/api/v1/media/images/image_cover1/image?width=200")

	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if config.Width != 256 {
		t.Fatalf("served width %d, want 256", config.Width)
	}
}

func TestImageRouteFallsBackToOriginalForUnusableWidths(t *testing.T) {
	handler, originalSize := imageRouteFixture(t, 300, 300)

	for _, url := range []string{
		// Above the top rung — the original is already the best answer.
		"/api/v1/media/images/image_cover1/image?width=4000",
		// Larger than the source: artwork is never upscaled.
		"/api/v1/media/images/image_cover1/image?width=512",
		// Not a width at all.
		"/api/v1/media/images/image_cover1/image?width=huge",
		"/api/v1/media/images/image_cover1/image?width=-5",
	} {
		_, body := fetchImage(t, handler, url)
		if int64(len(body)) != originalSize {
			t.Fatalf("%s served %d bytes, want the %d byte original",
				url, len(body), originalSize)
		}
	}
}

func TestImageRouteWithoutThumbnailServiceServesOriginal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	libraryDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := image.NewRGBA(image.Rect(0, 0, 800, 800))
	coverPath := filepath.Join(libraryDir, "cover.jpg")
	file, err := os.Create(coverPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(file, source, nil); err != nil {
		t.Fatal(err)
	}
	file.Close()
	info, _ := os.Stat(coverPath)

	db := storagetest.Open(t)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path)
		VALUES ('library-1', 'Music', 'music', '', ?)`, libraryDir); err != nil {
		t.Fatal(err)
	}

	// Thumbnails deliberately omitted: an older deployment, or one where the
	// directory could not be created, must keep serving artwork.
	handler := NewServer(ServerOptions{
		Catalog: catalog.NewService(catalog.Seed{
			MusicAlbums: []catalog.MusicAlbum{{
				ID:     "album-1",
				Images: []catalog.Image{{ID: "image_cover1", Path: coverPath}},
			}},
		}),
		Files: files.New(db),
	})

	_, body := fetchImage(t, handler, "/api/v1/media/images/image_cover1/image?width=128")
	if int64(len(body)) != info.Size() {
		t.Fatalf("served %d bytes, want the %d byte original", len(body), info.Size())
	}
}
