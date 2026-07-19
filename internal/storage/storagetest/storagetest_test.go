package storagetest

import (
	"context"
	"testing"
)

// TestOpenGivesIsolatedMigratedDatabases proves the clone plumbing: two Opens
// yield fully migrated databases that don't see each other's writes.
func TestOpenGivesIsolatedMigratedDatabases(t *testing.T) {
	ctx := context.Background()
	db1 := Open(t)
	db2 := Open(t)

	var applied int
	if err := db1.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("ledger query: %v", err)
	}
	if applied < 3 {
		t.Fatalf("expected the full migration lineage applied, got %d rows", applied)
	}

	if _, err := db1.ExecContext(ctx,
		`INSERT INTO libraries (id, name, kind, path) VALUES (?, ?, ?, ?)`,
		"lib1", "Music", "music", "/music"); err != nil {
		t.Fatalf("insert into db1: %v", err)
	}
	var count int
	if err := db2.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE id = ?`, "lib1").Scan(&count); err != nil {
		t.Fatalf("query db2: %v", err)
	}
	if count != 0 {
		t.Fatal("databases are not isolated: db2 sees db1's rows")
	}
}
