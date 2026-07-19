package storage_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// TestApplyMigrationsSkipsAppleDouble proves a macOS "._*.sql" sidecar in the
// migration set is ignored, not executed as SQL (the bug that broke a deploy).
func TestApplyMigrationsSkipsAppleDouble(t *testing.T) {
	dir := t.TempDir()
	// A migration named past the real lineage so it actually runs on the
	// already-migrated test database.
	if err := os.WriteFile(filepath.Join(dir, "0999_test.sql"),
		[]byte(`CREATE TABLE appledouble_probe (id TEXT PRIMARY KEY);`), 0o644); err != nil {
		t.Fatal(err)
	}
	// AppleDouble junk that sorts BEFORE it and contains a NUL byte (as the real
	// binary sidecars do). If applied, it corrupts the query.
	if err := os.WriteFile(filepath.Join(dir, "._0999_test.sql"),
		[]byte("\x00\x05\x16\x07binary junk not sql"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	db := storagetest.Open(t)

	if err := storage.ApplyMigrations(ctx, db, os.DirFS(dir)); err != nil {
		t.Fatalf("migrations should skip the AppleDouble file, got: %v", err)
	}
	// The real migration ran.
	if _, err := db.ExecContext(ctx, `INSERT INTO appledouble_probe (id) VALUES ('x')`); err != nil {
		t.Fatalf("real migration did not apply: %v", err)
	}
	// The junk was not recorded as applied.
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version LIKE '._%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("AppleDouble file was recorded as a migration (%d)", n)
	}
}
