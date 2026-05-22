package playback

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestPlaybackPatchUpdatesMusicTrack(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, duration_seconds, playback_json, added_at, updated_at)
		VALUES ('track-1', 'Signal One', 120, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	updated, err := service.Patch(ctx, TargetMusicTrack, "track-1", PatchInput{
		ProgressSeconds:     intPtr(42),
		Rating:              intPtr(5),
		Favorite:            boolPtr(true),
		TouchLastPositionAt: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProgressSeconds != 42 || !updated.Favorite || updated.Rating != 5 {
		t.Fatalf("unexpected state: %+v", updated)
	}

	loaded, err := service.Get(ctx, TargetMusicTrack, "track-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ProgressSeconds != 42 {
		t.Fatalf("loaded progress = %d, want 42", loaded.ProgressSeconds)
	}
}

func TestPlaybackRejectsInvalidRating(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, playback_json, added_at, updated_at)
		VALUES ('track-1', 'Signal One', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	_, err = service.Put(ctx, TargetMusicTrack, "track-1", catalog.PlaybackState{Rating: 9})
	if err == nil {
		t.Fatal("expected invalid rating error")
	}
}

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }
