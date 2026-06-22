package covers

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestUpsertAndLoadExtractedCover(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service, err := New(db, Options{CoverDir: filepath.Join(root, "covers")})
	if err != nil {
		t.Fatal(err)
	}

	image := catalog.Image{
		ID:       coverID("/music/track.flac"),
		Path:     filepath.Join(root, "covers", "art.jpg"),
		MimeType: "image/jpeg",
	}
	if err := service.upsert(ctx, "/music/track.flac", "checksum-1", image); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Get(ctx, image.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Path != image.Path {
		t.Fatalf("path = %q, want %q", loaded.Path, image.Path)
	}
}

func TestHasEmbeddedCoverDetectsPicardStyleEmbed(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}

	root := t.TempDir()
	source := filepath.Join(root, "picard.flac")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-f", "lavfi", "-i", "color=c=blue:s=500x500",
		"-map", "0:a", "-map", "1:v",
		"-c:a", "flac", "-c:v", "mjpeg", "-disposition:v:0", "attached_pic",
		"-shortest", source,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test flac: %v: %s", err, output)
	}

	if !hasEmbeddedCover(context.Background(), "ffprobe", source) {
		t.Skip("ffmpeg/ffprobe build did not expose attached picture for generated flac")
	}

	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	service, err := New(db, Options{CoverDir: filepath.Join(root, "covers")})
	if err != nil {
		t.Fatal(err)
	}
	image, err := service.ResolveForAudio(ctx, source, "checksum-test", nil)
	if err != nil {
		t.Fatalf("ResolveForAudio: %v", err)
	}
	if image == nil || !fileExists(image.Path) {
		t.Fatalf("expected extracted cover file, got %#v", image)
	}
}

func createSolidColorImage(t *testing.T, color, path string) {
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c="+color+":s=300x300", "-frames:v", "1", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test image: %v: %s", err, output)
	}
}

func TestComposite(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	root := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	service, err := New(db, Options{CoverDir: filepath.Join(root, "covers")})
	if err != nil {
		t.Fatal(err)
	}

	p1 := filepath.Join(root, "1.jpg")
	p2 := filepath.Join(root, "2.jpg")
	p3 := filepath.Join(root, "3.jpg")
	p4 := filepath.Join(root, "4.jpg")

	createSolidColorImage(t, "red", p1)
	createSolidColorImage(t, "green", p2)
	createSolidColorImage(t, "blue", p3)
	createSolidColorImage(t, "yellow", p4)

	image, err := service.Composite(ctx, "playlist-1", "hash-123", []string{p1, p2, p3, p4})
	if err != nil {
		t.Fatalf("Composite: %v", err)
	}
	if image == nil || !fileExists(image.Path) {
		t.Fatalf("expected composite cover file, got %#v", image)
	}

	// Should load from cache
	image2, err := service.Composite(ctx, "playlist-1", "hash-123", []string{p1, p2, p3, p4})
	if err != nil {
		t.Fatalf("Composite (cached): %v", err)
	}
	if image2.Path != image.Path {
		t.Fatalf("expected cached image path %q, got %q", image.Path, image2.Path)
	}
}
