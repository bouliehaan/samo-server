package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestSetPodcastCoverPersistsOverrideAndRow(t *testing.T) {
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
	cover := Image{ID: "cover_test", Path: coverPath, MimeType: "image/jpeg"}
	if err := SetPodcastCover(ctx, db, podcastID, cover); err != nil {
		t.Fatal(err)
	}

	var coverJSON string
	if err := db.QueryRowContext(ctx, `SELECT cover_json FROM podcasts WHERE id = ?`, podcastID).Scan(&coverJSON); err != nil {
		t.Fatal(err)
	}
	var stored Image
	if err := json.Unmarshal([]byte(coverJSON), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.ID != cover.ID || stored.Path != cover.Path {
		t.Fatalf("stored cover = %#v, want %#v", stored, cover)
	}

	record, err := GetMetadataOverride(ctx, db, OverrideKindPodcast, podcastID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := decodePatchImage(record.Fields, "cover")
	if !ok || decoded == nil || decoded.ID != cover.ID {
		t.Fatalf("override cover = %#v, ok=%v", decoded, ok)
	}
}
