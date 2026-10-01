package storage_test

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestPlaylistCompositeMigrationRefreshesOnlyLegacyPlaylistsOnce(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	const old = "2026-01-01T00:00:00Z"
	for _, id := range []string{"legacy", "versioned", "unrelated"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO music_playlists (id,name,track_ids_json,updated_at) VALUES (?,?,'["track-1","track-2"]',?)`, id, id, old); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{"composite:legacy", "composite:versioned:cover_revision"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO extracted_covers (id,source_path,path) VALUES (?,?,?)`, source, source, "/covers/old.jpg"); err != nil {
			t.Fatal(err)
		}
	}
	// The fixture already applied all migrations to an empty database. Replay
	// this one against an existing installation, just as a deployment does.
	if _, err := db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version='0028_playlist_composite_refresh.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"legacy", "versioned", "unrelated"} {
		var updated, tracks string
		if err := db.QueryRowContext(ctx, `SELECT updated_at,track_ids_json FROM music_playlists WHERE id=?`, id).Scan(&updated, &tracks); err != nil {
			t.Fatal(err)
		}
		if (updated != old) != (id == "legacy") {
			t.Errorf("%s updated_at = %q", id, updated)
		}
		if tracks != `["track-1","track-2"]` {
			t.Errorf("membership changed: %s", tracks)
		}
	}
	var covers int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM extracted_covers`).Scan(&covers); err != nil {
		t.Fatal(err)
	}
	if covers != 2 {
		t.Fatalf("old cover records were modified: count=%d", covers)
	}
	if _, err := db.ExecContext(ctx, `UPDATE music_playlists SET updated_at=? WHERE id='legacy'`, old); err != nil {
		t.Fatal(err)
	}
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	var again string
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM music_playlists WHERE id='legacy'`).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if again != old {
		t.Fatal("migration invalidates covers on every startup")
	}
}
