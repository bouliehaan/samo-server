package catalogstore

import (
	"context"
	"encoding/json"
	"github.com/bouliehaan/samo-server/internal/catalog"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestSetPodcastCoverPersistsOverrideAndRow(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db := storagetest.Open(t)

	podcastID := "podcast_cover"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path)
		VALUES ('lib_pod', 'Podcasts', 'podcast', ?)`, filepath.Join(root, "podcasts")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcasts (id, library_id, path, cover_json)
		VALUES (?, 'lib_pod', 'Show', '{}')`, podcastID); err != nil {
		t.Fatal(err)
	}

	coverPath := filepath.Join(root, "custom.jpg")
	if err := os.WriteFile(coverPath, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	cover := catalog.Image{ID: "cover_test", Path: coverPath, MimeType: "image/jpeg"}
	if err := SetPodcastCover(ctx, db, podcastID, cover); err != nil {
		t.Fatal(err)
	}

	var coverJSON string
	if err := db.QueryRowContext(ctx, `SELECT cover_json FROM podcasts WHERE id = ?`, podcastID).Scan(&coverJSON); err != nil {
		t.Fatal(err)
	}
	var stored catalog.Image
	if err := json.Unmarshal([]byte(coverJSON), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.ID != cover.ID || stored.Path != cover.Path {
		t.Fatalf("stored cover = %#v, want %#v", stored, cover)
	}

	record, err := GetMetadataOverride(ctx, db, catalog.OverrideKindPodcast, podcastID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := catalog.DecodePatchImage(record.Fields, "cover")
	if !ok || decoded == nil || decoded.ID != cover.ID {
		t.Fatalf("override cover = %#v, ok=%v", decoded, ok)
	}
}
