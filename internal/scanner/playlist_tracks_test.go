package scanner

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestReconcilePlaylistTrackReferencesRemapsStaleIDs(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	oldTrack := "track_old"
	newTrack := "track_new"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, updated_at)
		VALUES (?, 'Song', CURRENT_TIMESTAMP)`, newTrack); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_playlists (
		  id, name, owner_id, public, track_ids_json, track_count, duration_seconds, images_json, playback_json, created_at, updated_at
		)
		VALUES ('pl_1', 'Mix', 'user_1', 0, ?, 1, 0, '[]', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		jsonText([]string{oldTrack})); err != nil {
		t.Fatal(err)
	}

	scanner := New(db)
	scanner.noteTrackIDMigration(oldTrack, newTrack)
	updated, err := scanner.reconcilePlaylistTrackReferences(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}

	var raw string
	if err := db.QueryRowContext(ctx, `SELECT track_ids_json FROM music_playlists WHERE id = 'pl_1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	ids := decodeTrackIDList(raw)
	if len(ids) != 1 || ids[0] != newTrack {
		t.Fatalf("track_ids_json = %#v, want [%q]", ids, newTrack)
	}
}

func TestNoteTrackIDMigrationChains(t *testing.T) {
	s := &Scanner{trackIDMigrations: map[string]string{}}
	s.noteTrackIDMigration("a", "b")
	s.noteTrackIDMigration("b", "c")
	if got := s.resolveMigratedTrackID("a"); got != "c" {
		t.Fatalf("resolve a = %q, want c", got)
	}
}
