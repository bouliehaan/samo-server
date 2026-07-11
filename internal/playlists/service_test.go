package playlists

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/users"
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

// A system playlist (the explo "Explore" queue) is re-derived by the server on
// every reconcile pass, so client mutations would only be silently reverted.
// They must be refused loudly instead - even for the playlist's own owner.
func TestSystemPlaylistRejectsClientMutation(t *testing.T) {
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
	created, err := service.Create(ctx, "admin-1", CreateInput{
		Name:     "Explore",
		TrackIDs: []string{"track-1"},
		System:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Update(ctx, "admin-1", created.ID, UpdateInput{
		TrackIDs: []string{"track-1", "track-2"},
	}); err != ErrSystemPlaylist {
		t.Fatalf("owner update err = %v, want ErrSystemPlaylist", err)
	}
	if err := service.Delete(ctx, "admin-1", created.ID); err != ErrSystemPlaylist {
		t.Fatalf("owner delete err = %v, want ErrSystemPlaylist", err)
	}

	// The internal reconciler path still writes membership - and recomputes
	// the denormalized count/duration the clients render.
	updated, err := service.SetSystemTracks(ctx, created.ID, []string{"track-1", "track-2"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.TrackCount != 2 || updated.DurationSeconds != 300 {
		t.Fatalf("system update = %#v", updated)
	}

	// And the internal path refuses ordinary playlists, so it can't become a
	// backdoor around the ownership checks.
	normal, err := service.Create(ctx, "user-1", CreateInput{Name: "Night Mix"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetSystemTracks(ctx, normal.ID, []string{"track-1"}); err != ErrForbidden {
		t.Fatalf("non-system err = %v, want forbidden", err)
	}
}

func TestPlaylistImportCSVMatchesLocalTracks(t *testing.T) {
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
		INSERT INTO music_tracks (id, title, display_artist, album_title, duration_seconds)
		VALUES
		  ('track-1', 'One More Time', 'Daft Punk', 'Discovery', 320),
		  ('track-2', 'Harder Better Faster Stronger', 'Daft Punk', 'Discovery', 224)`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	result, err := service.Import(ctx, "user-1", ImportInput{
		Name:       "Robots",
		SourceType: "csv",
		Content:    "title,artist,album,duration\nOne More Time,Daft Punk,Discovery,5:20\nMissing Song,Daft Punk,Discovery,3:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Playlist == nil || result.Playlist.TrackCount != 1 {
		t.Fatalf("playlist = %#v", result.Playlist)
	}
	if result.MatchedCount != 1 || result.UnmatchedCount != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result.TrackIDs[0] != "track-1" {
		t.Fatalf("track ids = %#v", result.TrackIDs)
	}
}

func TestPlaylistImportM3UMatchesByPath(t *testing.T) {
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
		INSERT INTO libraries (id, name, kind, path)
		VALUES ('lib-1', 'Music', 'music', '/music');
		INSERT INTO music_tracks (id, title, display_artist, duration_seconds)
		VALUES ('track-1', 'Windowlicker', 'Aphex Twin', 364);
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds)
		VALUES ('file-1', 'lib-1', 'track-1', '/music/Aphex Twin/Windowlicker.flac', 'Aphex Twin/Windowlicker.flac', 'Windowlicker.flac', 364);`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	result, err := service.Import(ctx, "user-1", ImportInput{
		Name:       "M3U",
		SourceType: "m3u",
		Content:    "#EXTM3U\n#EXTINF:364,Aphex Twin - Windowlicker\n/music/Aphex Twin/Windowlicker.flac\n",
		DryRun:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Playlist != nil {
		t.Fatalf("dry run playlist = %#v", result.Playlist)
	}
	if result.MatchedCount != 1 || len(result.TrackIDs) != 1 || result.TrackIDs[0] != "track-1" {
		t.Fatalf("result = %#v", result)
	}
}

// TestFirstAdminOwnerIDPrefersHumanAdmin is the regression for the invisible
// Explo playlist: the internal bootstrap account (users.BootstrapUserID) has a
// zero created_at, so a plain ORDER BY created_at always elected it - and a
// non-public playlist it owned rendered for nobody, since playlists are only
// visible to their owner. The earliest HUMAN admin must win; the bootstrap
// account is a last resort only.
func TestFirstAdminOwnerIDPrefersHumanAdmin(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	// Migration 008 seeds the bootstrap admin; give it the zero created_at it
	// carries on servers bootstrapped by older code, which is what made it sort
	// first and win the old ORDER BY created_at.
	if _, err := db.ExecContext(ctx, `
		UPDATE users SET created_at = '0001-01-01 00:00:00' WHERE id = '`+users.BootstrapUserID+`';
		INSERT INTO users (id, username, role, created_at) VALUES
		  ('user-human', 'jake', 'admin', '2026-05-23 21:26:34'),
		  ('user-later', 'katie-admin', 'admin', '2026-07-02 06:45:35'),
		  ('user-plain', 'norm', 'user', '2020-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}

	got, err := FirstAdminOwnerID(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if got != "user-human" {
		t.Fatalf("FirstAdminOwnerID = %q, want the earliest human admin %q", got, "user-human")
	}

	// With no human admin left, the bootstrap account is still a valid owner.
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id IN ('user-human', 'user-later')`); err != nil {
		t.Fatal(err)
	}
	got, err = FirstAdminOwnerID(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if got != users.BootstrapUserID {
		t.Fatalf("FirstAdminOwnerID with only the bootstrap admin = %q, want %q", got, users.BootstrapUserID)
	}
}
