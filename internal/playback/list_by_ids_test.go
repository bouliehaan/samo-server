package playback

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestListForUserByIDsReturnsOnlyRequestedRows(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, role, password_hash)
		VALUES ('user-1', 'listener', 'Listener', 'user', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, playback_json, added_at, updated_at)
		VALUES
			('track-a', 'A', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			('track-b', 'B', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	if _, err := service.Patch(ctx, "user-1", TargetMusicTrack, "track-a", PatchInput{
		IncrementPlayCount: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Patch(ctx, "user-1", TargetMusicTrack, "track-b", PatchInput{
		IncrementPlayCount: true,
	}); err != nil {
		t.Fatal(err)
	}

	states, err := service.ListForUserByIDs(ctx, "user-1", TargetMusicTrack, []string{"track-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 {
		t.Fatalf("states = %d, want 1", len(states))
	}
	if states["track-a"].PlayCount != 1 {
		t.Fatalf("track-a playCount = %d, want 1", states["track-a"].PlayCount)
	}
	if _, ok := states["track-b"]; ok {
		t.Fatal("track-b should not be returned")
	}
}
