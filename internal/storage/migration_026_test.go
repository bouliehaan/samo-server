package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

// TestMigration026AddsMillisecondColumns guards that the audiobook precision
// migration applies cleanly on top of the full migration history and that every
// new column it introduces is queryable.
func TestMigration026AddsMillisecondColumns(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	for _, query := range []string{
		"SELECT duration_ms FROM media_files LIMIT 0",
		"SELECT start_ms, end_ms FROM audiobook_chapters LIMIT 0",
		"SELECT start_ms, end_ms FROM episode_chapters LIMIT 0",
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatalf("expected column present (%q): %v", query, err)
		}
	}

	// Re-applying must be a no-op (the migration is recorded as applied).
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatalf("re-apply migrations: %v", err)
	}
}
