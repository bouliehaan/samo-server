package covers

import (
	"context"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestCompositeRebuildsAfterPlaylistArtworkChanges(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	ctx := context.Background()
	db := storagetest.Open(t)
	root := t.TempDir()
	service, err := New(db, Options{CoverDir: filepath.Join(root, "covers")})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	palette := []color.RGBA{{R: 255, A: 255}, {G: 255, A: 255}, {B: 255, A: 255}, {R: 255, G: 255, A: 255}}
	for i, c := range palette {
		im := image.NewRGBA(image.Rect(0, 0, 20, 20))
		for y := 0; y < 20; y++ {
			for x := 0; x < 20; x++ {
				im.Set(x, y, c)
			}
		}
		path := filepath.Join(root, string(rune('a'+i))+".png")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, im)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	const playlistID = "playlist_existing"
	first, err := service.Composite(ctx, playlistID, "a,b,c,d", paths)
	if err != nil {
		t.Fatal(err)
	}
	// Existing installations already have a row with the unversioned source
	// key. Preserve it: old image IDs must continue resolving to their bytes.
	if err := service.upsert(ctx, "composite:"+playlistID, "a,b,c,d", *first); err != nil {
		t.Fatal(err)
	}
	reordered := []string{paths[3], paths[2], paths[1], paths[0]}
	second, err := service.Composite(ctx, playlistID, "d,c,b,a", reordered)
	if err != nil {
		t.Fatalf("rebuild existing playlist collage: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("changed covers reused the old image ID")
	}
	old, err := service.Get(ctx, first.ID)
	if err != nil || old.Path != first.Path || !fileExists(old.Path) {
		t.Fatalf("previous cover was lost: %+v, %v", old, err)
	}
	f, err := os.Open(second.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoded, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 600 || decoded.Bounds().Dy() != 600 {
		t.Fatalf("not a collage: %v", decoded.Bounds())
	}
	for i := range palette {
		want := palette[3-i]
		got := color.RGBAModel.Convert(decoded.At(150+(i%2)*300, 150+(i/2)*300)).(color.RGBA)
		near := func(a, b uint8) bool { d := int(a) - int(b); return d >= -12 && d <= 12 }
		if !near(got.R, want.R) || !near(got.G, want.G) || !near(got.B, want.B) {
			t.Errorf("quadrant %d = %v, want %v", i, got, want)
		}
	}
	// An unchanged revision should reuse the image without rendering again.
	service.ffmpegPath = filepath.Join(root, "missing-ffmpeg")
	cached, err := service.Composite(ctx, playlistID, "d,c,b,a", reordered)
	if err != nil || cached.ID != second.ID {
		t.Fatalf("cache miss for unchanged art: %+v, %v", cached, err)
	}
	// Rebuilding a lost file must also upsert the same revision successfully.
	if err := os.Remove(second.Path); err != nil {
		t.Fatal(err)
	}
	service.ffmpegPath = "ffmpeg"
	rebuilt, err := service.Composite(ctx, playlistID, "d,c,b,a", reordered)
	if err != nil || rebuilt.ID != second.ID || !fileExists(rebuilt.Path) {
		t.Fatalf("lost-file rebuild: %+v, %v", rebuilt, err)
	}
}
