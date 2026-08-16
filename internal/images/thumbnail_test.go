package images

import (
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writeJPEG(t *testing.T, dir string, w, h int) string {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			// A gradient rather than a flat fill, so a resize that silently
			// point-sampled a single pixel would still look plausible but a
			// wrong average would not.
			source.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	path := filepath.Join(dir, "source.jpg")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	defer file.Close()
	if err := jpeg.Encode(file, source, nil); err != nil {
		t.Fatalf("encode source: %v", err)
	}
	return path
}

func statSource(t *testing.T, path string) (int64, int64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat source: %v", err)
	}
	return info.ModTime().Unix(), info.Size()
}

func decodeSize(t *testing.T, path string) (int, int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open variant: %v", err)
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		t.Fatalf("decode variant: %v", err)
	}
	return config.Width, config.Height
}

func TestVariantDownscalesAndCaches(t *testing.T) {
	dir := t.TempDir()
	source := writeJPEG(t, dir, 1400, 1400)
	modTime, size := statSource(t, source)

	service, err := New(filepath.Join(dir, "thumbs"))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	path, err := service.Variant(source, modTime, size, 128)
	if err != nil {
		t.Fatalf("variant: %v", err)
	}

	if w, h := decodeSize(t, path); w != 128 || h != 128 {
		t.Fatalf("expected 128x128 variant, got %dx%d", w, h)
	}

	sourceInfo, _ := os.Stat(source)
	variantInfo, _ := os.Stat(path)
	if variantInfo.Size() >= sourceInfo.Size() {
		t.Fatalf("variant (%d bytes) should be smaller than source (%d bytes)",
			variantInfo.Size(), sourceInfo.Size())
	}

	// A second call must reuse the rendered file rather than write a new one.
	again, err := service.Variant(source, modTime, size, 128)
	if err != nil {
		t.Fatalf("second variant: %v", err)
	}
	if again != path {
		t.Fatalf("expected cache hit at %s, got %s", path, again)
	}
	entries, err := os.ReadDir(service.Dir())
	if err != nil {
		t.Fatalf("read thumbnail dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one cached variant, found %d", len(entries))
	}
}

func TestVariantKeyChangesWhenArtworkIsReplaced(t *testing.T) {
	dir := t.TempDir()
	source := writeJPEG(t, dir, 512, 512)
	modTime, size := statSource(t, source)

	service, err := New(filepath.Join(dir, "thumbs"))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	first, err := service.Variant(source, modTime, size, 128)
	if err != nil {
		t.Fatalf("variant: %v", err)
	}

	// Same path, different bytes — the variant must not be reused, or a
	// re-scanned library would keep showing the artwork it replaced.
	second, err := service.Variant(source, modTime+1, size, 128)
	if err != nil {
		t.Fatalf("variant after replacement: %v", err)
	}
	if second == first {
		t.Fatal("replaced artwork reused the previous variant")
	}
}

func TestVariantPreservesAspectRatio(t *testing.T) {
	dir := t.TempDir()
	source := writeJPEG(t, dir, 1000, 500)
	modTime, size := statSource(t, source)

	service, err := New(filepath.Join(dir, "thumbs"))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	path, err := service.Variant(source, modTime, size, 256)
	if err != nil {
		t.Fatalf("variant: %v", err)
	}
	if w, h := decodeSize(t, path); w != 256 || h != 128 {
		t.Fatalf("expected 256x128 variant, got %dx%d", w, h)
	}
}

func TestVariantRefusesToUpscale(t *testing.T) {
	dir := t.TempDir()
	source := writeJPEG(t, dir, 96, 96)
	modTime, size := statSource(t, source)

	service, err := New(filepath.Join(dir, "thumbs"))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	if _, err := service.Variant(source, modTime, size, 256); err != ErrNotSmaller {
		t.Fatalf("expected ErrNotSmaller, got %v", err)
	}
}

func TestVariantRejectsUndecodableSource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "cover.webp")
	if err := os.WriteFile(source, []byte("RIFF....WEBPVP8 not really"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	modTime, size := statSource(t, source)

	service, err := New(filepath.Join(dir, "thumbs"))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	if _, err := service.Variant(source, modTime, size, 128); err != ErrUnsupported {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestVariantKeepsPNGSourcesAsPNG(t *testing.T) {
	dir := t.TempDir()
	source := image.NewRGBA(image.Rect(0, 0, 512, 512))
	for y := range 512 {
		for x := range 512 {
			source.Set(x, y, color.RGBA{R: uint8(x % 256), G: 64, B: uint8(y % 256), A: 255})
		}
	}
	path := filepath.Join(dir, "cover.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := png.Encode(file, source); err != nil {
		t.Fatalf("encode source: %v", err)
	}
	file.Close()
	modTime, size := statSource(t, path)

	service, err := New(filepath.Join(dir, "thumbs"))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	variant, err := service.Variant(path, modTime, size, 128)
	if err != nil {
		t.Fatalf("variant: %v", err)
	}
	if filepath.Ext(variant) != ".png" {
		t.Fatalf("expected a .png variant, got %s", variant)
	}
}

func TestDownscaleAveragesRatherThanSamples(t *testing.T) {
	// A checkerboard averages to a uniform mid-grey. Point sampling would
	// return pure black or pure white for every output pixel instead.
	const size = 64
	source := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			shade := uint8(0)
			if (x+y)%2 == 0 {
				shade = 255
			}
			source.Set(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}

	result := downscale(source, 8, 8)
	for y := range 8 {
		for x := range 8 {
			r, _, _, _ := result.At(x, y).RGBA()
			got := r >> 8
			if got < 120 || got > 136 {
				t.Fatalf("pixel (%d,%d) = %d, expected a mid-grey average", x, y, got)
			}
		}
	}
}

func TestNormalizeWidthSnapsUpTheLadder(t *testing.T) {
	cases := []struct {
		raw     string
		want    int
		wantAny bool
	}{
		{raw: "48", want: 64, wantAny: true},
		{raw: "64", want: 64, wantAny: true},
		{raw: "65", want: 128, wantAny: true},
		{raw: "200", want: 256, wantAny: true},
		{raw: "1024", want: 1024, wantAny: true},
		// Above the top rung the original is already the right answer.
		{raw: "2048", wantAny: false},
		{raw: "", wantAny: false},
		{raw: "0", wantAny: false},
		{raw: "-10", wantAny: false},
		{raw: "wide", wantAny: false},
	}

	for _, tc := range cases {
		got, ok := NormalizeWidth(tc.raw)
		if ok != tc.wantAny {
			t.Fatalf("NormalizeWidth(%q) ok = %v, want %v", tc.raw, ok, tc.wantAny)
		}
		if ok && got != tc.want {
			t.Fatalf("NormalizeWidth(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}
