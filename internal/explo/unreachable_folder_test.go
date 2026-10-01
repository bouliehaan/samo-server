package explo

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

// seedDrop puts two explo albums in the catalog under drop, one track each,
// hidden and flagged the way a processed weekly drop is. With onDisk, the
// folder exists and holds the first track's file; the second has rotated out.
func seedDrop(t *testing.T, db *sql.DB, drop string, onDisk bool) {
	t.Helper()
	ctx := context.Background()
	if err := users.New(users.ServiceOptions{DB: db}).Bootstrap(ctx, users.BootstrapInput{AdminUsername: "owner", AdminPassword: "owner-pass-123"}); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO libraries (id, name, kind, path) VALUES ('lib-1', 'Music', 'music', ?)`, []any{filepath.Dir(drop)}},
		{`INSERT INTO music_albums (id, title, track_count, hidden_from_recently_added) VALUES ('album-a', 'Homewrecker', 1, 1), ('album-b', 'Beatopia', 1, 1)`, nil},
		{`INSERT INTO music_tracks (id, title, album_id, is_explo) VALUES ('track-a', 'Homewrecker', 'album-a', 1), ('track-b', 'Talk', 'album-b', 1)`, nil},
		{`INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name)
		  VALUES ('file-a', 'lib-1', 'track-a', ?, 'a.flac', 'a.flac'), ('file-b', 'lib-1', 'track-b', ?, 'b.flac', 'b.flac')`,
			[]any{filepath.Join(drop, "a.flac"), filepath.Join(drop, "b.flac")}},
		{`INSERT INTO explo_tracks (track_id, status) VALUES ('track-a', 'matched'), ('track-b', 'matched')`, nil},
	} {
		if _, err := db.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if !onDisk {
		return
	}
	if err := os.MkdirAll(drop, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drop, "a.flac"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dropService(db *sql.DB, drop string) *Service {
	return NewService(ServiceOptions{
		DB:            db,
		Dirs:          []string{drop},
		MetadataApply: metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{}),
		Playlists:     playlists.New(db),
	})
}

func count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 2026-10-01: samo was started once without the disk the explo folder lives
// on. Every drop file stat'd as missing, the prune deleted every explo track,
// and the un-hide put each emptied album on the Home shelf. A folder samo
// cannot see must leave its tracks alone.
func TestPruneLeavesAnUnreachableFolderAlone(t *testing.T) {
	db := storagetest.Open(t)
	unmounted := filepath.Join(t.TempDir(), "not-mounted", "explo", "Weekly-Exploration")
	seedDrop(t, db, unmounted, false)

	service := dropService(db, unmounted)
	pruned, err := service.PruneRotatedOutFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 || count(t, db, `SELECT COUNT(*) FROM music_tracks`) != 2 || count(t, db, `SELECT COUNT(*) FROM explo_tracks`) != 2 {
		t.Fatalf("an unreachable folder was read as rotated out: pruned %d", pruned)
	}
	if err := service.ReconcileRecentlyAdded(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM music_albums WHERE hidden_from_recently_added = 0`); n != 0 {
		t.Fatalf("%d explo album(s) leaked into the library", n)
	}
}

// Real rotation still prunes, and takes the emptied album with it rather
// than leaving an empty row the library would show.
func TestPruneRemovesRotatedOutFilesAndTheirEmptiedAlbums(t *testing.T) {
	db := storagetest.Open(t)
	drop := filepath.Join(t.TempDir(), "explo", "Weekly-Exploration")
	seedDrop(t, db, drop, true)

	service := dropService(db, drop)
	pruned, err := service.PruneRotatedOutFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned %d, want only the missing file", pruned)
	}
	if count(t, db, `SELECT COUNT(*) FROM music_albums WHERE id = 'album-b'`) != 0 {
		t.Fatal("rotated-out album outlived its only track")
	}
	if count(t, db, `SELECT COUNT(*) FROM music_albums WHERE id = 'album-a' AND hidden_from_recently_added = 1`) != 1 {
		t.Fatal("album still in the drop lost its row or its silo flag")
	}
}

// An album row with no files at all is not library. Un-hiding it is how the
// emptied albums reached Home as blank tiles.
func TestReconcileKeepsFilelessAlbumsHidden(t *testing.T) {
	db := storagetest.Open(t)
	drop := filepath.Join(t.TempDir(), "explo", "Weekly-Exploration")
	seedDrop(t, db, drop, true)
	if _, err := db.Exec(`DELETE FROM music_tracks WHERE id = 'track-b'`); err != nil {
		t.Fatal(err)
	}
	// A library album that was wrongly hidden still comes back.
	if _, err := db.Exec(`UPDATE media_files SET path = '/library/Homewrecker/a.flac' WHERE id = 'file-a'`); err != nil {
		t.Fatal(err)
	}

	if err := dropService(db, drop).ReconcileRecentlyAdded(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count(t, db, `SELECT hidden_from_recently_added FROM music_albums WHERE id = 'album-b'`) != 1 {
		t.Fatal("an album with no tracks was un-hidden into the library")
	}
	if count(t, db, `SELECT hidden_from_recently_added FROM music_albums WHERE id = 'album-a'`) != 0 {
		t.Fatal("a library album outside the folder stayed hidden")
	}
}
