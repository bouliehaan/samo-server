package explo

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/users"
	"github.com/bouliehaan/samo-server/migrations"
)

func newMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query); err != nil {
		t.Fatalf("seed exec failed: %v", err)
	}
}

func assertHidden(t *testing.T, db *sql.DB, albumID string, want bool) {
	t.Helper()
	var hidden int
	if err := db.QueryRowContext(context.Background(), `SELECT hidden_from_recently_added FROM music_albums WHERE id = ?`, albumID).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if (hidden != 0) != want {
		t.Fatalf("album %s hidden = %v, want %v", albumID, hidden != 0, want)
	}
}

// seedPathAlbums lays out two albums: one whose files live under /music/explo,
// one whose files live under /music/real, plus an empty album. Enough to prove
// the path-based hide/un-hide logic.
func seedPathAlbums(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `
		INSERT INTO libraries (id, name, kind, path) VALUES ('lib-1', 'Music', 'music', '/music');
		INSERT INTO music_albums (id, title, track_count, duration_seconds, updated_at) VALUES
		  ('album-explo', 'Explo', 2, 0, '2020-01-01 00:00:00'),
		  ('album-real', 'Real', 2, 0, '2020-01-01 00:00:00'),
		  ('album-empty', 'Empty', 0, 0, '2020-01-01 00:00:00');
		INSERT INTO music_tracks (id, title, display_artist, album_id, duration_seconds) VALUES
		  ('e1', 'a', 'x', 'album-explo', 200),
		  ('e2', 'b', 'x', 'album-explo', 200),
		  ('r1', 'c', 'y', 'album-real', 200),
		  ('r2', 'd', 'y', 'album-real', 200);
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds) VALUES
		  ('mf-e1', 'lib-1', 'e1', '/music/explo/e1.mp3', 'explo/e1.mp3', 'e1.mp3', 200),
		  ('mf-e2', 'lib-1', 'e2', '/music/explo/e2.mp3', 'explo/e2.mp3', 'e2.mp3', 200),
		  ('mf-r1', 'lib-1', 'r1', '/music/real/r1.mp3', 'real/r1.mp3', 'r1.mp3', 200),
		  ('mf-r2', 'lib-1', 'r2', '/music/real/r2.mp3', 'real/r2.mp3', 'r2.mp3', 200);
	`)
}

// TestReconcileHidesAlbumsUnderExploFolder covers the core path-based rule:
// hide an album only when every one of its files is under an explo folder, and
// bump updated_at only on the rows that actually change.
func TestReconcileHidesAlbumsUnderExploFolder(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)
	seedPathAlbums(t, db)
	svc := &Service{db: db}

	hidden, unhidden, err := svc.reconcileHiddenAlbums(ctx, []string{"/music/explo"})
	if err != nil {
		t.Fatal(err)
	}
	if hidden != 1 || unhidden != 0 {
		t.Fatalf("hidden=%d unhidden=%d, want 1/0 (only album-explo)", hidden, unhidden)
	}
	assertHidden(t, db, "album-explo", true)  // all files under /music/explo
	assertHidden(t, db, "album-real", false)  // files under /music/real
	assertHidden(t, db, "album-empty", false) // no files

	// updated_at bumped on the hidden album (so Android re-syncs), untouched on
	// the others (no spurious sync churn).
	var exploUpdated, realUpdated string
	_ = db.QueryRowContext(ctx, `SELECT updated_at FROM music_albums WHERE id='album-explo'`).Scan(&exploUpdated)
	_ = db.QueryRowContext(ctx, `SELECT updated_at FROM music_albums WHERE id='album-real'`).Scan(&realUpdated)
	if exploUpdated == "2020-01-01 00:00:00" {
		t.Fatal("hidden album updated_at must advance so the mirror re-syncs")
	}
	if realUpdated != "2020-01-01 00:00:00" {
		t.Fatalf("untouched album updated_at changed to %q", realUpdated)
	}

	// Idempotent: nothing flips on a second identical pass.
	if h, u, _ := svc.reconcileHiddenAlbums(ctx, []string{"/music/explo"}); h != 0 || u != 0 {
		t.Fatalf("second pass hidden=%d unhidden=%d, want 0/0", h, u)
	}
}

// TestReconcileUnhidesWhenFolderNarrowsOrClears is the regression for the
// real-albums-vanished bug: a too-broad folder hides everything, and the fix
// is that pointing the folder at the real drop subfolder (or clearing it)
// un-hides the albums that aren't actually under it.
func TestReconcileUnhidesWhenFolderNarrowsOrClears(t *testing.T) {
	ctx := context.Background()
	db := newMigratedDB(t)
	seedPathAlbums(t, db)
	svc := &Service{db: db}

	// Too-broad folder: /music matches BOTH albums, so both get hidden.
	if h, _, err := svc.reconcileHiddenAlbums(ctx, []string{"/music"}); err != nil || h != 2 {
		t.Fatalf("broad hide = (%d, %v), want (2, nil)", h, err)
	}
	assertHidden(t, db, "album-explo", true)
	assertHidden(t, db, "album-real", true)

	// Narrow to the real drop folder: album-real is no longer under it -> back.
	h, u, err := svc.reconcileHiddenAlbums(ctx, []string{"/music/explo"})
	if err != nil {
		t.Fatal(err)
	}
	if h != 0 || u != 1 {
		t.Fatalf("narrow reconcile hidden=%d unhidden=%d, want 0/1", h, u)
	}
	assertHidden(t, db, "album-explo", true)
	assertHidden(t, db, "album-real", false)

	// Clearing the folder entirely un-hides everything.
	if _, u, _ := svc.reconcileHiddenAlbums(ctx, nil); u != 1 {
		t.Fatalf("clear un-hid %d, want 1 (album-explo)", u)
	}
	assertHidden(t, db, "album-explo", false)
	assertHidden(t, db, "album-real", false)
}

// TestSyncExploStatePrunesLedgerAndPlaylist proves that narrowing the folder
// also removes wrongly-swept tracks from the explo_tracks ledger and the Explo
// playlist, not just the hidden flags.
func TestSyncExploStatePrunesLedgerAndPlaylist(t *testing.T) {
	ctx := context.Background()
	db, _ := setupExploTestDB(t) // seeds album-explo (track-matched, track-unmatched) under /music/explo + admin
	// Add a real album/track under /music/real, then simulate a too-broad run
	// that swept ALL three tracks into the ledger + Explo playlist.
	mustExec(t, db, `
		INSERT INTO music_albums (id, title, track_count, duration_seconds) VALUES ('album-real', 'Real', 1, 0);
		INSERT INTO music_tracks (id, title, display_artist, album_id, duration_seconds) VALUES ('real-1', 'R', 'y', 'album-real', 200);
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds)
		  VALUES ('mf-real-1', 'lib-1', 'real-1', '/music/real/r.mp3', 'real/r.mp3', 'r.mp3', 200);
		INSERT INTO explo_tracks (track_id, status) VALUES ('track-matched','matched'),('track-unmatched','unmatched'),('real-1','matched');
	`)
	svc := newConfigTestService(t, db, []string{"/music/explo"}, "env-key")
	ownerID, err := playlists.FirstAdminOwnerID(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.playlists.Create(ctx, ownerID, playlists.CreateInput{
		Name:     DefaultPlaylistName,
		System:   true,
		TrackIDs: []string{"track-matched", "track-unmatched", "real-1"},
	}); err != nil {
		t.Fatal(err)
	}

	// Sync to the correct narrow folder.
	if _, _, _, err := svc.syncExploState(ctx, []string{"/music/explo"}); err != nil {
		t.Fatal(err)
	}

	// The real track is gone from the ledger; the explo ones remain.
	var ledger int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM explo_tracks WHERE track_id='real-1'`).Scan(&ledger)
	if ledger != 0 {
		t.Fatal("real track should be pruned from explo_tracks")
	}
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM explo_tracks`).Scan(&ledger)
	if ledger != 2 {
		t.Fatalf("explo_tracks = %d, want 2 (only the explo-folder tracks)", ledger)
	}

	// The real track is gone from the Explo playlist; the album is not hidden.
	var trackIDsJSON string
	if err := db.QueryRowContext(ctx, `SELECT track_ids_json FROM music_playlists WHERE name=? AND system=1`, DefaultPlaylistName).Scan(&trackIDsJSON); err != nil {
		t.Fatal(err)
	}
	var ids []string
	_ = json.Unmarshal([]byte(trackIDsJSON), &ids)
	if len(ids) != 2 {
		t.Fatalf("playlist tracks = %v, want the 2 explo-folder tracks", ids)
	}
	for _, id := range ids {
		if id == "real-1" {
			t.Fatal("real track must be pruned from the Explo playlist")
		}
	}
	assertHidden(t, db, "album-real", false)
	assertHidden(t, db, "album-explo", true)
}

// TestProcessNewTracksHidesUnderFolderWithNoNewCandidates proves the reconcile
// runs even when there is nothing new to identify - an explo album stays out of
// Recently Added on a pass that scans no fresh drops.
func TestProcessNewTracksHidesUnderFolderWithNoNewCandidates(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	// Both tracks already recorded, so findCandidateTracks returns nothing, but
	// the path-based reconcile must still hide the album.
	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status) VALUES ('track-matched', 'matched'), ('track-unmatched', 'unmatched');
		UPDATE music_albums SET hidden_from_recently_added = 0 WHERE id = 'album-explo';
	`)
	svc := newConfigTestService(t, db, []string{exploDir}, "env-key")

	res, err := svc.ProcessNewTracks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 0 {
		t.Fatalf("scanned = %d, want 0", res.Scanned)
	}
	if res.Hidden != 1 {
		t.Fatalf("hidden = %d, want 1", res.Hidden)
	}
	assertHidden(t, db, "album-explo", true)
}

// TestReconcileRecreatesMissingExploPlaylist is the regression for the
// vanished "Explore" queue: the playlist row is gone (deleted, or never
// visible because it was created under the wrong owner and cleaned up) while
// the ledger still knows every processed drop. The reconcile must rebuild the
// playlist from the ledger on the next pass instead of waiting for a fresh
// weekly drop to trigger a create.
func TestReconcileRecreatesMissingExploPlaylist(t *testing.T) {
	ctx := context.Background()
	db, _ := setupExploTestDB(t)
	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status) VALUES
		  ('track-matched', 'matched'), ('track-unmatched', 'unmatched');
	`)
	svc := newConfigTestService(t, db, []string{"/music/explo"}, "env-key")

	changed, err := svc.reconcileExploPlaylist(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("reconcile must report a change when it creates the playlist")
	}

	var ownerID, trackIDsJSON string
	if err := db.QueryRowContext(ctx, `
		SELECT owner_id, track_ids_json FROM music_playlists WHERE name = ? AND system = 1`,
		DefaultPlaylistName).Scan(&ownerID, &trackIDsJSON); err != nil {
		t.Fatalf("playlist not recreated: %v", err)
	}
	var humanAdmin string
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = 'owner'`).Scan(&humanAdmin); err != nil {
		t.Fatal(err)
	}
	if ownerID != humanAdmin {
		t.Fatalf("recreated playlist owner = %q, want the human admin %q", ownerID, humanAdmin)
	}
	var ids []string
	_ = json.Unmarshal([]byte(trackIDsJSON), &ids)
	if len(ids) != 2 {
		t.Fatalf("recreated playlist tracks = %v, want both ledger tracks", ids)
	}

	// Idempotent: an identical second pass writes nothing.
	changed, err = svc.reconcileExploPlaylist(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second identical reconcile must be a no-op")
	}

	// An empty ledger with no playlist row must not create an empty playlist.
	mustExec(t, db, `DELETE FROM explo_tracks; DELETE FROM music_playlists;`)
	changed, err = svc.reconcileExploPlaylist(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("empty ledger + no playlist must stay a no-op")
	}
	var count int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_playlists`).Scan(&count)
	if count != 0 {
		t.Fatal("no playlist should be created from an empty ledger")
	}
}

// TestReconcileAdoptsBootstrapOwnedExploPlaylist reproduces the production
// bug exactly: FirstAdminOwnerID used to elect the internal bootstrap admin
// (zero created_at sorts first), so the playlist was created owned by
// users.BootstrapUserID - and since non-public playlists only render for
// their owner, no client ever saw it. Reconcile must re-own it to a human
// admin and top up its membership from the ledger.
func TestReconcileAdoptsBootstrapOwnedExploPlaylist(t *testing.T) {
	ctx := context.Background()
	db, _ := setupExploTestDB(t)
	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status) VALUES
		  ('track-matched', 'matched'), ('track-unmatched', 'unmatched');
	`)
	svc := newConfigTestService(t, db, []string{"/music/explo"}, "env-key")

	// The playlist as production created it: bootstrap-owned, partial members.
	if _, err := svc.playlists.Create(ctx, users.BootstrapUserID, playlists.CreateInput{
		Name:     DefaultPlaylistName,
		System:   true,
		TrackIDs: []string{"track-matched"},
	}); err != nil {
		t.Fatal(err)
	}

	changed, err := svc.reconcileExploPlaylist(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("reconcile must report the adoption")
	}

	var ownerID, trackIDsJSON string
	if err := db.QueryRowContext(ctx, `
		SELECT owner_id, track_ids_json FROM music_playlists WHERE name = ? AND system = 1`,
		DefaultPlaylistName).Scan(&ownerID, &trackIDsJSON); err != nil {
		t.Fatal(err)
	}
	if ownerID == users.BootstrapUserID {
		t.Fatal("playlist must be re-owned away from the bootstrap admin")
	}
	var humanAdmin string
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = 'owner'`).Scan(&humanAdmin); err != nil {
		t.Fatal(err)
	}
	if ownerID != humanAdmin {
		t.Fatalf("adopted owner = %q, want %q", ownerID, humanAdmin)
	}
	var ids []string
	_ = json.Unmarshal([]byte(trackIDsJSON), &ids)
	if len(ids) != 2 || ids[0] != "track-matched" {
		t.Fatalf("membership after adoption = %v, want existing order kept + missing ledger track appended", ids)
	}

	// A human-owned playlist is never stolen: run again, owner stays put.
	changed, err = svc.reconcileExploPlaylist(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("stable adopted playlist must reconcile as a no-op")
	}
}

// TestReconcileAdoptsAndRenamesOldDefaultPlaylist covers the 2026-07-09
// default rename ("Explo" -> "Explore"): a server that already created the
// system playlist under the old name must have that row adopted and renamed
// in place - same id, same members - never duplicated. Recognition is by the
// system flag, not the name.
func TestReconcileAdoptsAndRenamesOldDefaultPlaylist(t *testing.T) {
	ctx := context.Background()
	db, _ := setupExploTestDB(t)
	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status) VALUES
		  ('track-matched', 'matched'), ('track-unmatched', 'unmatched');
	`)
	svc := newConfigTestService(t, db, []string{"/music/explo"}, "env-key")
	if svc.playlistName == "Explo" {
		t.Fatal("test requires the configured name to differ from the legacy one")
	}

	var humanAdmin string
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = 'owner'`).Scan(&humanAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.playlists.Create(ctx, humanAdmin, playlists.CreateInput{
		Name:     "Explo", // the pre-rename default
		System:   true,
		TrackIDs: []string{"track-matched", "track-unmatched"},
	}); err != nil {
		t.Fatal(err)
	}
	var legacyID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM music_playlists WHERE system = 1`).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}

	changed, err := svc.reconcileExploPlaylist(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("reconcile must report the rename")
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_playlists WHERE system = 1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("system playlist count = %d, want exactly 1 (no duplicate under the new name)", count)
	}
	var gotName, trackIDsJSON string
	if err := db.QueryRowContext(ctx, `
		SELECT name, track_ids_json FROM music_playlists WHERE id = ?`,
		legacyID).Scan(&gotName, &trackIDsJSON); err != nil {
		t.Fatalf("legacy playlist row disappeared (renamed row must keep its id): %v", err)
	}
	if gotName != svc.playlistName {
		t.Fatalf("playlist name = %q, want renamed to %q", gotName, svc.playlistName)
	}
	var ids []string
	_ = json.Unmarshal([]byte(trackIDsJSON), &ids)
	if len(ids) != 2 || ids[0] != "track-matched" {
		t.Fatalf("membership after rename = %v, want unchanged", ids)
	}

	// Second pass is a clean no-op.
	changed, err = svc.reconcileExploPlaylist(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("renamed playlist must reconcile as a no-op on the next pass")
	}
}
