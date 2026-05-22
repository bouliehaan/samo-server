package storage

import (
	"context"
	"testing"

	"github.com/jakedebus/samo-server/migrations"
)

func TestApplyMigrationsIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = '001_init.sql'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration count = %d, want 1", count)
	}
}
