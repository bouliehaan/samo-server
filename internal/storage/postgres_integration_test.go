package storage_test

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// TestPostgresEndToEnd exercises the dialect surface the codebase leans on:
// `?` placeholder rewriting, COLLATE nocase, ON CONFLICT upserts, IDENTITY
// columns, RFC3339 text timestamp defaults, FK cascades, and the json_extract
// compatibility function.
func TestPostgresEndToEnd(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	t.Run("placeholders_and_upsert", func(t *testing.T) {
		// `?` placeholders must reach Postgres as `$1..$n`. This also exercises
		// the ON CONFLICT upsert form used across the codebase.
		const ins = `INSERT INTO radio_stations (id, name, description, content_type, epoch, enabled, source)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET name = excluded.name`
		if _, err := db.ExecContext(ctx, ins, "st1", "Jazz FM", "smooth", "audio/mpeg", "1970-01-01T00:00:00Z", 1, "database"); err != nil {
			t.Fatalf("insert radio station: %v", err)
		}
		// Upsert: same id, new name.
		if _, err := db.ExecContext(ctx, ins, "st1", "Jazz FM 2", "smooth", "audio/mpeg", "1970-01-01T00:00:00Z", 1, "database"); err != nil {
			t.Fatalf("upsert radio station: %v", err)
		}
		var name string
		if err := db.QueryRowContext(ctx, `SELECT name FROM radio_stations WHERE id = ?`, "st1").Scan(&name); err != nil {
			t.Fatalf("select station: %v", err)
		}
		if name != "Jazz FM 2" {
			t.Fatalf("upsert did not update: got %q", name)
		}
	})

	t.Run("collate_nocase", func(t *testing.T) {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO users (id, username, role) VALUES (?, ?, ?)`,
			"u1", "Bob", "admin"); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		// Case-insensitive lookup, mirroring the login path.
		var id string
		err := db.QueryRowContext(ctx,
			`SELECT id FROM users WHERE username = ? COLLATE nocase`, "bOB").Scan(&id)
		if err != nil {
			t.Fatalf("nocase lookup failed: %v", err)
		}
		if id != "u1" {
			t.Fatalf("nocase lookup returned %q", id)
		}
		// Case-insensitive uniqueness must reject a case variant.
		_, err = db.ExecContext(ctx,
			`INSERT INTO users (id, username, role) VALUES (?, ?, ?)`,
			"u2", "BOB", "user")
		if err == nil {
			t.Fatal("expected unique violation on case-variant username")
		}
	})

	t.Run("identity_column", func(t *testing.T) {
		// lastfm_scrobble_queue.id is GENERATED AS IDENTITY: insert without id.
		if _, err := db.ExecContext(ctx,
			`INSERT INTO lastfm_scrobble_queue (kind, artist, track, timestamp, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			"scrobble", "Miles Davis", "So What", 1234567890, "2026-07-12T00:00:00Z"); err != nil {
			t.Fatalf("insert scrobble: %v", err)
		}
		var id int64
		if err := db.QueryRowContext(ctx,
			`SELECT id FROM lastfm_scrobble_queue WHERE artist = ?`, "Miles Davis").Scan(&id); err != nil {
			t.Fatalf("select scrobble id: %v", err)
		}
		if id <= 0 {
			t.Fatalf("identity did not assign a positive id: %d", id)
		}
	})

	t.Run("timestamp_default_is_rfc3339", func(t *testing.T) {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO libraries (id, name, kind, path) VALUES (?, ?, ?, ?)`,
			"lib1", "Music", "music", "/music"); err != nil {
			t.Fatalf("insert library: %v", err)
		}
		var createdAt string
		if err := db.QueryRowContext(ctx,
			`SELECT created_at FROM libraries WHERE id = ?`, "lib1").Scan(&createdAt); err != nil {
			t.Fatalf("select created_at: %v", err)
		}
		// Must parse with the same RFC3339 the Go code uses to read timestamps.
		if len(createdAt) != len("2006-01-02T15:04:05Z") || createdAt[10] != 'T' || createdAt[len(createdAt)-1] != 'Z' {
			t.Fatalf("created_at default is not RFC3339 UTC: %q", createdAt)
		}
	})

	t.Run("foreign_key_cascade", func(t *testing.T) {
		// Referential integrity survives the topo-sorted schema.
		if _, err := db.ExecContext(ctx,
			`INSERT INTO radio_station_items (id, station_id, position, source_kind) VALUES (?, ?, ?, ?)`,
			"item1", "st1", 0, "track"); err != nil {
			t.Fatalf("insert station item: %v", err)
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM radio_station_items`).Scan(&count); err != nil {
			t.Fatalf("count items: %v", err)
		}
		if count != 1 {
			t.Fatalf("expected 1 item, got %d", count)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM radio_stations WHERE id = ?`, "st1"); err != nil {
			t.Fatalf("delete station: %v", err)
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM radio_station_items`).Scan(&count); err != nil {
			t.Fatalf("recount items: %v", err)
		}
		if count != 0 {
			t.Fatalf("ON DELETE CASCADE did not fire: %d items remain", count)
		}
	})

	t.Run("json_extract_compat", func(t *testing.T) {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO libraries (id, name, kind, path) VALUES (?, ?, ?, ?)
			 ON CONFLICT(id) DO NOTHING`,
			"libj", "MusicJ", "music", "/musicj"); err != nil {
			t.Fatalf("insert library: %v", err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO media_files (id, library_id, path, embedded_tags_json) VALUES (?, ?, ?, ?)`,
			"mf1", "libj", "/musicj/a.flac", `{"musicbrainz_trackid":"abc-123","musicbrainz_recordingid":"rec-999"}`); err != nil {
			t.Fatalf("insert media file: %v", err)
		}
		// The exact shape the scanner's cross-library matcher runs.
		var id string
		err := db.QueryRowContext(ctx, `
			SELECT id FROM media_files
			WHERE json_extract(embedded_tags_json, '$.musicbrainz_trackid') = ?
			   OR json_extract(embedded_tags_json, '$.musicbrainz_recordingid') = ?`,
			"abc-123", "nope").Scan(&id)
		if err != nil {
			t.Fatalf("json_extract query failed: %v", err)
		}
		if id != "mf1" {
			t.Fatalf("json_extract matched wrong row: %q", id)
		}
		// Missing key returns NULL (no match), not an error.
		var n int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM media_files
			WHERE json_extract(embedded_tags_json, '$.absent') = ?`, "x").Scan(&n); err != nil {
			t.Fatalf("json_extract missing-key query failed: %v", err)
		}
		if n != 0 {
			t.Fatalf("expected 0 matches for absent key, got %d", n)
		}
	})
}
