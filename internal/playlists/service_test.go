package playlists

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestPlaylistCreateUpdateDelete(t *testing.T) {
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
		INSERT INTO music_tracks (id, title, duration_seconds)
		VALUES ('track-1', 'One', 120), ('track-2', 'Two', 180)`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	created, err := service.Create(ctx, "user-1", CreateInput{
		Name:     "Night Mix",
		TrackIDs: []string{"track-1", "track-2"},
		Public:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.TrackCount != 2 || created.DurationSeconds != 300 {
		t.Fatalf("created = %#v", created)
	}

	name := "Night Mix v2"
	updated, err := service.Update(ctx, "user-1", created.ID, UpdateInput{
		Name:     &name,
		TrackIDs: []string{"track-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != name || updated.TrackCount != 1 || updated.DurationSeconds != 180 {
		t.Fatalf("updated = %#v", updated)
	}

	if err := service.Delete(ctx, "user-1", created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.loadByID(ctx, created.ID); err != ErrNotFound {
		t.Fatalf("delete err = %v", err)
	}
}

func TestPlaylistUpdateRejectsOtherOwner(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	created, err := service.Create(ctx, "user-1", CreateInput{Name: "Private Mix"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, "user-2", created.ID, UpdateInput{}); err != ErrForbidden {
		t.Fatalf("err = %v, want forbidden", err)
	}
}
