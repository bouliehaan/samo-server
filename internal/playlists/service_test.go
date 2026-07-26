package playlists

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

func TestPlaylistCreateUpdateDelete(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
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
	db := storagetest.Open(t)

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
	db := storagetest.Open(t)
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
	db := storagetest.Open(t)
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
	db := storagetest.Open(t)
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
// Filesystem imports and rows migrated from older servers are owned by the
// internal bootstrap account no human can authenticate as. Owner-only delete
// made them permanently undeletable from every surface; an admin may remove
// any non-system playlist, while non-admins stay locked out.
func TestPlaylistAdminDeletesServerOwnedPlaylist(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, role, created_at) VALUES
		  ('user-admin', 'jake', 'admin', '2026-05-23 21:26:34'),
		  ('user-plain', 'norm', 'user', '2026-05-24 08:00:00')`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	imported, err := service.Create(ctx, users.BootstrapUserID, CreateInput{Name: "Migrated Mix"})
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Delete(ctx, "user-plain", imported.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin delete err = %v, want ErrForbidden", err)
	}
	if err := service.Delete(ctx, "user-admin", imported.ID); err != nil {
		t.Fatalf("admin delete err = %v", err)
	}
	if _, err := service.loadByID(ctx, imported.ID); err != ErrNotFound {
		t.Fatalf("after admin delete, load err = %v, want ErrNotFound", err)
	}
}

// Deleting a playlist must stick: the scanner re-imports every on-disk .m3u
// each full scan, which would silently resurrect deliberately deleted
// playlists. A delete writes a name-keyed tombstone the scan path honors; a
// manual API import is an explicit request, so it clears the tombstone and
// scan passes may maintain the playlist again.
func TestPlaylistDeleteTombstoneBlocksScanReimport(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, role, created_at)
		VALUES ('user-admin', 'jake', 'admin', '2026-05-23 21:26:34')`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	path := filepath.Join(t.TempDir(), "Road Trip.m3u")
	if err := os.WriteFile(path, []byte("#EXTM3U\n/music/gone.mp3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	imported, err := service.ImportM3UFromPath(ctx, "user-admin", path)
	if err != nil || !imported {
		t.Fatalf("initial import = %v, %v", imported, err)
	}
	id := playlistID("user-admin", "Road Trip")
	if _, err := service.loadByID(ctx, id); err != nil {
		t.Fatalf("imported playlist missing: %v", err)
	}
	if err := service.Delete(ctx, "user-admin", id); err != nil {
		t.Fatal(err)
	}

	imported, err = service.ImportM3UFromPath(ctx, "user-admin", path)
	if err != nil {
		t.Fatal(err)
	}
	if imported {
		t.Fatal("scan re-import resurrected a deleted playlist")
	}
	if _, err := service.loadByID(ctx, id); err != ErrNotFound {
		t.Fatalf("after tombstoned re-import, load err = %v, want ErrNotFound", err)
	}

	if _, err := service.Import(ctx, "user-admin", ImportInput{
		Name:       "Road Trip",
		SourceType: "m3u",
		Content:    "#EXTM3U\n/music/gone.mp3\n",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.loadByID(ctx, id); err != nil {
		t.Fatalf("manual import after delete: load err = %v", err)
	}
	if _, err := service.ImportM3UFromPath(ctx, "user-admin", path); err != nil {
		t.Fatalf("scan import after manual restore: %v", err)
	}
}

// Explo playlist: the internal bootstrap account (users.BootstrapUserID) has a
// zero created_at, so a plain ORDER BY created_at always elected it - and a
// non-public playlist it owned rendered for nobody, since playlists are only
// visible to their owner. The earliest HUMAN admin must win; the bootstrap
// account is a last resort only.
func TestFirstAdminOwnerIDPrefersHumanAdmin(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
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
