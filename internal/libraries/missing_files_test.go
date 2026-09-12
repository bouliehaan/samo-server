package libraries

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestRemoveAllMissingFiles(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	svc := New(db, scanner.New(db))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, updated_at)
		VALUES ('lib1', 'Music', 'music', '', '/music', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		id, path string
		missing  int
	}{
		{"f1", "/music/a.flac", 1},
		{"f2", "/music/b.flac", 1},
		{"f3", "/music/c.flac", 0},
	} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO media_files (id, library_id, path, relative_path, file_name, missing, updated_at)
			VALUES (?, 'lib1', ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
			spec.id, spec.path, filepath.Base(spec.path), filepath.Base(spec.path), spec.missing); err != nil {
			t.Fatal(err)
		}
	}

	result, err := svc.RemoveAllMissingFiles(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 2 {
		t.Fatalf("removed = %d, want 2", result.Removed)
	}
	page, err := svc.ListMissingFiles(ctx, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("missing total = %d, want 0", page.Total)
	}
	var remain int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_files`).Scan(&remain); err != nil {
		t.Fatal(err)
	}
	if remain != 1 {
		t.Fatalf("media_files count = %d, want 1", remain)
	}
}

// The workflow the settings panel drives: a scan flags a file it can no longer
// find, the file shows up in the missing-files list, and removing it takes the
// catalog rows behind it with it.
func TestMissingFilesListThenRemoveClearsCatalogRows(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	svc := New(db, scanner.New(db))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, updated_at)
		VALUES ('lib1', 'Music', 'music', '', '/music', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_artists (id, name, updated_at) VALUES ('art1', 'deadmau5', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_albums (id, title, display_artist, updated_at)
		VALUES ('alb1', 'Random Album Title', 'deadmau5', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, album_id, updated_at)
		VALUES ('trk1', 'Faxing Berlin', 'alb1', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, missing, missing_detected_at, updated_at)
		VALUES ('f1', 'lib1', 'trk1', '/music/deadmau5/01.flac', 'deadmau5/01.flac', '01.flac', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	page, err := svc.ListMissingFiles(ctx, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("missing list total=%d items=%d, want 1 and 1", page.Total, len(page.Items))
	}
	if page.Items[0].AlbumTitle != "Random Album Title" || page.Items[0].TrackTitle != "Faxing Berlin" {
		t.Fatalf("missing item = %+v, want the track and album titles filled in", page.Items[0])
	}

	if _, err := svc.RemoveAllMissingFiles(ctx, ""); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		label string
		query string
	}{
		{"media_files", `SELECT COUNT(*) FROM media_files WHERE id = 'f1'`},
		{"music_tracks", `SELECT COUNT(*) FROM music_tracks WHERE id = 'trk1'`},
		{"music_albums", `SELECT COUNT(*) FROM music_albums WHERE id = 'alb1'`},
		{"music_artists", `SELECT COUNT(*) FROM music_artists WHERE id = 'art1'`},
	} {
		var count int
		if err := db.QueryRowContext(ctx, check.query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows = %d, want 0 after the review removal", check.label, count)
		}
	}
}
