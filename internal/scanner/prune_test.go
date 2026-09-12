package scanner

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// seedTrackWithFile puts one artist/album/track/media_file chain in the
// catalog, pointing at path.
func seedTrackWithFile(t *testing.T, ctx context.Context, sc *Scanner, libraryID, suffix, artistName, albumTitle, path string) {
	t.Helper()
	if err := sc.upsertMusicArtist(ctx, catalog.MusicArtist{ID: "artist-" + suffix, Name: artistName}); err != nil {
		t.Fatal(err)
	}
	if err := sc.upsertMusicAlbum(ctx, catalog.MusicAlbum{ID: "album-" + suffix, Title: albumTitle, DisplayArtist: artistName}); err != nil {
		t.Fatal(err)
	}
	if err := sc.upsertMusicTrack(ctx, catalog.MusicTrack{ID: "track-" + suffix, Title: "Track", AlbumID: "album-" + suffix, DurationSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	if err := sc.upsertAudioFile(ctx, libraryID, audioFileOwner{TrackID: "track-" + suffix}, catalog.AudioFile{
		ID:       "file-" + suffix,
		Path:     path,
		FileName: filepath.Base(path),
	}, "", ""); err != nil {
		t.Fatal(err)
	}
}

func countRow(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPruneFlagsVanishedFilesAndDropsExcludedOnes(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	root := t.TempDir()
	keptPath := filepath.Join(root, "current.flac")
	vanishedPath := filepath.Join(root, "vanished.flac")
	excludedPath := filepath.Join(root, "excluded.flac")
	// keptPath is walked; excludedPath is on disk but not walked (an ignore
	// rule); vanishedPath was deleted.
	for _, path := range []string{keptPath, excludedPath} {
		if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	scanner := New(db)
	library := Library{ID: "library-1", Name: "Music", Kind: "music", Path: root}
	if err := scanner.upsertLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	seedTrackWithFile(t, ctx, scanner, library.ID, "kept", "The Signal", "Daylight", keptPath)
	seedTrackWithFile(t, ctx, scanner, library.ID, "vanished", "The Static", "Night Broadcasts", vanishedPath)
	seedTrackWithFile(t, ctx, scanner, library.ID, "excluded", "The Hiss", "Off Air", excludedPath)

	accumulator := newScanAccumulator()
	accumulator.seeFile(keptPath)
	stats, err := scanner.pruneLibrary(ctx, library, accumulator)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesMarked != 1 {
		t.Fatalf("files marked missing = %d, want 1 — a deleted file waits for review, it is not dropped", stats.FilesMarked)
	}
	if stats.FilesPruned != 1 {
		t.Fatalf("files pruned = %d, want 1 — a file still on disk but excluded from the walk is not missing", stats.FilesPruned)
	}

	if got := countRow(t, ctx, db, `SELECT COUNT(*) FROM media_files WHERE path = ? AND missing = 1`, vanishedPath); got != 1 {
		t.Fatalf("vanished file flagged missing = %d, want 1", got)
	}
	if got := countRow(t, ctx, db, `SELECT COUNT(*) FROM media_files WHERE path = ?`, excludedPath); got != 0 {
		t.Fatalf("excluded media_files rows = %d, want 0", got)
	}
	// The catalog keeps the vanished track until somebody reviews it.
	if got := countRow(t, ctx, db, `SELECT COUNT(*) FROM music_tracks WHERE id = 'track-vanished'`); got != 1 {
		t.Fatalf("vanished track count = %d, want 1 pending review", got)
	}
}

// Deleting a folder has to surface in the missing-files list, where it can be
// reviewed and removed on purpose.
//
// The bug this pins down: phase 2 flags every path the walk did not see as
// missing so it can pair moved files by persistent id, and prune then only
// looked at rows with missing = 0. That was survivable on its own, but it also
// meant prune re-marked nothing and could never revisit those rows — and the
// stat that decided their fate was inverted, so a file proven gone was treated
// as an unreachable mount while a mid-scan I/O error deleted rows outright.
func TestPruneFlagsAlbumFolderDeletedFromDisk(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	root := t.TempDir()
	keptDir := filepath.Join(root, "Other Artist", "Some Album")
	deletedDir := filepath.Join(root, "Deleted Artist", "Some Album")
	for _, dir := range []string{keptDir, deletedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	keptPath := filepath.Join(keptDir, "01.flac")
	deletedPath := filepath.Join(deletedDir, "01.flac")
	for _, path := range []string{keptPath, deletedPath} {
		if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	scanner := New(db)
	library := Library{ID: "library-1", Name: "Music", Kind: "music", Path: root}
	if err := scanner.upsertLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	seedTrackWithFile(t, ctx, scanner, library.ID, "kept", "Other Artist", "Some Album", keptPath)
	seedTrackWithFile(t, ctx, scanner, library.ID, "gone", "Deleted Artist", "Some Album", deletedPath)

	if err := os.RemoveAll(filepath.Join(root, "Deleted Artist")); err != nil {
		t.Fatal(err)
	}

	accumulator := newScanAccumulator()
	accumulator.seeFile(keptPath)

	// What the pipeline does before prune: flag every unseen path missing.
	if _, err := scanner.markUnseenMediaFilesMissing(ctx, library.ID, accumulator.filePaths); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.pruneLibrary(ctx, library, accumulator); err != nil {
		t.Fatal(err)
	}

	if got := countRow(t, ctx, db, `SELECT COUNT(*) FROM media_files WHERE id = 'file-gone' AND missing = 1`); got != 1 {
		t.Fatalf("deleted file flagged missing = %d, want 1 — it has to reach the review list", got)
	}
	if got := countRow(t, ctx, db, `SELECT COUNT(*) FROM media_files WHERE id = 'file-kept' AND missing = 0`); got != 1 {
		t.Fatalf("surviving file rows = %d, want 1 unflagged", got)
	}
}

// An unplugged drive must not empty the library. The root stat is what
// separates "the files are gone" from "the volume is gone".
func TestPruneKeepsEverythingWhenLibraryRootIsUnreachable(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	root := filepath.Join(t.TempDir(), "unmounted")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	trackPath := filepath.Join(root, "song.flac")
	if err := os.WriteFile(trackPath, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	scanner := New(db)
	library := Library{ID: "library-1", Name: "Music", Kind: "music", Path: root}
	if err := scanner.upsertLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	seedTrackWithFile(t, ctx, scanner, library.ID, "one", "The Signal", "Daylight", trackPath)

	// The volume goes away underneath us.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	// A walk that still reported files — a stale listing, a partial mount —
	// gets past the empty-walk guard, so the root check is the only thing
	// standing between an unplugged drive and an emptied library.
	accumulator := newScanAccumulator()
	accumulator.seeFile(filepath.Join(root, "something-else.flac"))

	stats, err := scanner.pruneLibrary(ctx, library, accumulator)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesPruned != 0 {
		t.Fatalf("files pruned = %d, want 0 when the library root is gone", stats.FilesPruned)
	}
	if got := countRow(t, ctx, db, `SELECT COUNT(*) FROM media_files WHERE id = 'file-one'`); got != 1 {
		t.Fatalf("media_files rows = %d, want 1 — an unreachable root must not delete anything", got)
	}
}

// A playlist row holding an empty string in track_ids_json must not fail the
// orphan prune. The column is NOT NULL DEFAULT '[]', so that takes a bad write
// to produce, and one unreadable playlist must not take every other library's
// prune down with it.
func TestPruneOrphanMusicSurvivesEmptyPlaylistTrackIDs(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	scanner := New(db)
	// The DELETE only evaluates its playlist subquery if there is something to
	// delete, so the orphan track is what makes this test exercise the cast.
	if err := scanner.upsertMusicTrack(ctx, catalog.MusicTrack{ID: "track-orphan", Title: "Unreferenced"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_playlists (id, name, track_ids_json)
		VALUES ('playlist-empty', 'Broken', '')`); err != nil {
		t.Fatal(err)
	}

	pruned, err := scanner.pruneOrphanMusic(ctx)
	if err != nil {
		t.Fatalf("prune with empty track_ids_json: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1 (the orphan track)", pruned)
	}
}

func TestScanWithStatsTracksSeenFiles(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	scanner := New(db)
	library := Library{ID: "library-1", Name: "Music", Kind: "music", Path: filepath.Clean("/music")}
	if err := scanner.upsertLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	accumulator := newScanAccumulator()
	accumulator.seeFile("/music/song.flac")
	scanner.activeScan = accumulator

	if err := scanner.upsertAudioFile(ctx, library.ID, audioFileOwner{}, catalog.AudioFile{
		ID:       "file-1",
		Path:     "/music/song.flac",
		FileName: "song.flac",
	}, "", ""); err != nil {
		t.Fatal(err)
	}
	if len(accumulator.filePaths) != 1 {
		t.Fatalf("seen files = %d, want 1", len(accumulator.filePaths))
	}
}

// A playlist reference used to keep a fileless track alive forever, and the
// track kept its album and artist alive with it — the blank, unplayable
// entries that survived every scan and every "remove missing files" click.
// Now the playlist gives up the dead entry and the rows go.
func TestPruneOrphanMusicDropsFilelessTrackFromPlaylists(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	sc := New(db)

	for _, stmt := range []string{
		`INSERT INTO music_artists (id, name, updated_at) VALUES ('art1', 'Gone Artist', CURRENT_TIMESTAMP)`,
		`INSERT INTO music_albums (id, title, display_artist, updated_at) VALUES ('alb1', 'Gone Album', 'Gone Artist', CURRENT_TIMESTAMP)`,
		`INSERT INTO music_tracks (id, title, album_id, updated_at) VALUES ('trk-dead', 'Deleted', 'alb1', CURRENT_TIMESTAMP)`,
		`INSERT INTO music_tracks (id, title, album_id, updated_at) VALUES ('trk-live', 'Still Here', 'alb1', CURRENT_TIMESTAMP)`,
		`INSERT INTO music_track_artists (track_id, artist_id, role, position) VALUES ('trk-dead', 'art1', 'artist', 0)`,
		`INSERT INTO music_album_artists (album_id, artist_id, position) VALUES ('alb1', 'art1', 0)`,
		`INSERT INTO music_playlists (id, name, track_ids_json, track_count) VALUES ('pl1', 'My Mix', '["trk-dead","trk-live"]', 2)`,
		`INSERT INTO libraries (id, name, kind, media_type, path, updated_at) VALUES ('lib1', 'Music', 'music', '', '/music', CURRENT_TIMESTAMP)`,
		// Only trk-live still has a file behind it.
		`INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, updated_at)
		   VALUES ('f-live', 'lib1', 'trk-live', '/music/live.flac', 'live.flac', 'live.flac', CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if _, err := sc.pruneOrphanMusic(ctx); err != nil {
		t.Fatal(err)
	}

	if got := countRow(t, ctx, db, `SELECT COUNT(*) FROM music_tracks WHERE id = 'trk-dead'`); got != 0 {
		t.Fatalf("fileless track rows = %d, want 0", got)
	}
	if got := countRow(t, ctx, db, `SELECT COUNT(*) FROM music_tracks WHERE id = 'trk-live'`); got != 1 {
		t.Fatalf("track with a file = %d, want 1", got)
	}

	var trackIDs string
	var trackCount int
	if err := db.QueryRowContext(ctx,
		`SELECT track_ids_json, track_count FROM music_playlists WHERE id = 'pl1'`).Scan(&trackIDs, &trackCount); err != nil {
		t.Fatal(err)
	}
	if trackIDs != `["trk-live"]` {
		t.Fatalf("playlist track ids = %s, want [\"trk-live\"]", trackIDs)
	}
	if trackCount != 1 {
		t.Fatalf("playlist track_count = %d, want 1", trackCount)
	}
	// The album keeps a live track, so it and its artist stay.
	if got := countRow(t, ctx, db, `SELECT COUNT(*) FROM music_albums WHERE id = 'alb1'`); got != 1 {
		t.Fatalf("album rows = %d, want 1", got)
	}
}

// The whole point of the review click: a flagged file is removed, and the blank
// entry behind it goes too even though a playlist named the track.
func TestPruneOrphanMusicClearsPlaylistHeldAlbumWhenAllFilesGone(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	sc := New(db)

	for _, stmt := range []string{
		`INSERT INTO music_artists (id, name, updated_at) VALUES ('art1', 'deadmau5', CURRENT_TIMESTAMP)`,
		`INSERT INTO music_albums (id, title, display_artist, updated_at) VALUES ('alb1', 'Random Album Title', 'deadmau5', CURRENT_TIMESTAMP)`,
		`INSERT INTO music_tracks (id, title, album_id, updated_at) VALUES ('trk1', 'Faxing Berlin', 'alb1', CURRENT_TIMESTAMP)`,
		`INSERT INTO music_track_artists (track_id, artist_id, role, position) VALUES ('trk1', 'art1', 'artist', 0)`,
		`INSERT INTO music_album_artists (album_id, artist_id, position) VALUES ('alb1', 'art1', 0)`,
		`INSERT INTO music_playlists (id, name, track_ids_json, track_count) VALUES ('pl1', 'Mix', '["trk1"]', 1)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if _, err := sc.pruneOrphanMusic(ctx); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ label, query string }{
		{"tracks", `SELECT COUNT(*) FROM music_tracks WHERE id = 'trk1'`},
		{"albums", `SELECT COUNT(*) FROM music_albums WHERE id = 'alb1'`},
		{"artists", `SELECT COUNT(*) FROM music_artists WHERE id = 'art1'`},
	} {
		if got := countRow(t, ctx, db, check.query); got != 0 {
			t.Fatalf("%s rows = %d, want 0 — the blank entry must not survive", check.label, got)
		}
	}
}

// Removing some of an artist's files must not take the rest of their catalog
// with it. Nothing here cascades by name: a track goes only when no media file
// points at it, an album goes only when no track is left in it, and an artist
// goes only when no track and no album still credit them.
func TestPruneOrphanMusicKeepsPartiallyDeletedArtistAndAlbum(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	sc := New(db)

	for _, stmt := range []string{
		`INSERT INTO libraries (id, name, kind, media_type, path, updated_at) VALUES ('lib1','Music','music','','/music',CURRENT_TIMESTAMP)`,
		`INSERT INTO music_artists (id, name, updated_at) VALUES ('art-mau5', 'deadmau5', CURRENT_TIMESTAMP)`,
		// Album A: one track deleted, one kept.
		`INSERT INTO music_albums (id, title, display_artist, updated_at) VALUES ('alb-a', 'Random Album Title', 'deadmau5', CURRENT_TIMESTAMP)`,
		`INSERT INTO music_tracks (id, title, album_id, updated_at) VALUES ('trk-a1', 'Deleted Track', 'alb-a', CURRENT_TIMESTAMP)`,
		`INSERT INTO music_tracks (id, title, album_id, updated_at) VALUES ('trk-a2', 'Kept Track', 'alb-a', CURRENT_TIMESTAMP)`,
		// Album B: every track deleted.
		`INSERT INTO music_albums (id, title, display_artist, updated_at) VALUES ('alb-b', '4x4=12', 'deadmau5', CURRENT_TIMESTAMP)`,
		`INSERT INTO music_tracks (id, title, album_id, updated_at) VALUES ('trk-b1', 'Also Deleted', 'alb-b', CURRENT_TIMESTAMP)`,
		`INSERT INTO music_track_artists (track_id, artist_id, role, position) VALUES ('trk-a1','art-mau5','artist',0)`,
		`INSERT INTO music_track_artists (track_id, artist_id, role, position) VALUES ('trk-a2','art-mau5','artist',0)`,
		`INSERT INTO music_track_artists (track_id, artist_id, role, position) VALUES ('trk-b1','art-mau5','artist',0)`,
		`INSERT INTO music_album_artists (album_id, artist_id, position) VALUES ('alb-a','art-mau5',0)`,
		`INSERT INTO music_album_artists (album_id, artist_id, position) VALUES ('alb-b','art-mau5',0)`,
		// Only the kept track still has a file on disk.
		`INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, updated_at)
		   VALUES ('f-a2','lib1','trk-a2','/music/deadmau5/kept.flac','deadmau5/kept.flac','kept.flac',CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if _, err := sc.pruneOrphanMusic(ctx); err != nil {
		t.Fatal(err)
	}

	for _, check := range []struct {
		label string
		query string
		want  int
	}{
		{"deleted track in the part-kept album", `SELECT COUNT(*) FROM music_tracks WHERE id = 'trk-a1'`, 0},
		{"kept track", `SELECT COUNT(*) FROM music_tracks WHERE id = 'trk-a2'`, 1},
		{"part-kept album", `SELECT COUNT(*) FROM music_albums WHERE id = 'alb-a'`, 1},
		{"fully deleted album", `SELECT COUNT(*) FROM music_albums WHERE id = 'alb-b'`, 0},
		{"artist with one surviving track", `SELECT COUNT(*) FROM music_artists WHERE id = 'art-mau5'`, 1},
	} {
		if got := countRow(t, ctx, db, check.query); got != check.want {
			t.Fatalf("%s = %d, want %d", check.label, got, check.want)
		}
	}
}
