package covers

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestCoverIDIsDeterministic(t *testing.T) {
	first := coverID("/music/album/track.flac")
	second := coverID("/music/album/track.flac")
	if first != second {
		t.Fatalf("cover id = %q vs %q, want deterministic", first, second)
	}
	if first == "" || first[:6] != "cover_" {
		t.Fatalf("cover id = %q, want cover_ prefix", first)
	}
}

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
