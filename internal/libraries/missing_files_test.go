package libraries

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestRemoveAllMissingFiles(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	svc := New(db, scanner.New(db))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, updated_at)
		VALUES ('lib1', 'Music', 'music', '', '/music', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		id, path string
		missing  int
	}{
		{"f1", "/music/a.flac", 1},
		{"f2", "/music/b.flac", 1},
		{"f3", "/music/c.flac", 0},
	} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO media_files (id, library_id, path, relative_path, file_name, missing, updated_at)
			VALUES (?, 'lib1', ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
			spec.id, spec.path, filepath.Base(spec.path), filepath.Base(spec.path), spec.missing); err != nil {
			t.Fatal(err)
		}
	}

	result, err := svc.RemoveAllMissingFiles(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 2 {
		t.Fatalf("removed = %d, want 2", result.Removed)
	}
	page, err := svc.ListMissingFiles(ctx, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("missing total = %d, want 0", page.Total)
	}
	var remain int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_files`).Scan(&remain); err != nil {
		t.Fatal(err)
	}
	if remain != 1 {
		t.Fatalf("media_files count = %d, want 1", remain)
	}
}
