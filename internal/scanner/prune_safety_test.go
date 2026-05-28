package scanner

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestPruneSkipsWhenWalkFindsNoFilesButLibraryHasRows(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	scanner := New(db)
	library := Library{ID: "lib-audio", Name: "Books", Kind: "audiobook", Path: "/books"}
	if err := scanner.upsertLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO audiobooks (id, library_id, path, updated_at)
		VALUES ('ab1', 'lib-audio', '/books/Title', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, audiobook_id, path, relative_path, file_name, missing, updated_at)
		VALUES ('f1', 'lib-audio', 'ab1', '/books/Title/part.m4b', 'Title/part.m4b', 'part.m4b', 0, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	stats, err := scanner.pruneLibrary(ctx, library, newScanAccumulator())
	if err != nil {
		t.Fatal(err)
	}
	if stats.ItemsPruned != 0 {
		t.Fatalf("items pruned = %d, want 0 when walk was empty", stats.ItemsPruned)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audiobooks WHERE library_id = 'lib-audio'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audiobook count = %d, want 1", count)
	}
}

func TestQuickScanMarksUnchangedAudiobookForPrune(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	scanner := New(db)
	library := Library{ID: "lib-audio", Name: "Books", Kind: "audiobook", Path: "/books"}
	if err := scanner.upsertLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	bookRoot := "/books/Author/Title"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO audiobooks (id, library_id, path, updated_at)
		VALUES ('ab1', 'lib-audio', ?, CURRENT_TIMESTAMP)`, bookRoot); err != nil {
		t.Fatal(err)
	}
	part := bookRoot + "/01.m4b"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, audiobook_id, path, relative_path, file_name, checksum, missing, updated_at)
		VALUES ('f1', 'lib-audio', 'ab1', ?, '01.m4b', '01.m4b', 'same', 0, CURRENT_TIMESTAMP)`, part); err != nil {
		t.Fatal(err)
	}

	accumulator := newScanAccumulator()
	scanner.activeScan = accumulator
	scanner.scanMode = ScanModeQuick
	index, err := scanner.loadFileIndex(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	scanner.fileIndex = index
	group := groupedAudio{Root: bookRoot, Files: []string{part}}
	scanner.markAudiobookGroupSeen(library.ID, group)
	if len(accumulator.audiobookIDs) == 0 {
		t.Fatal("quick scan must mark audiobook as seen for prune")
	}
	stats, err := scanner.pruneLibrary(ctx, library, accumulator)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ItemsPruned != 0 {
		t.Fatalf("items pruned = %d, want 0", stats.ItemsPruned)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audiobooks WHERE library_id = ?`, library.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audiobook count = %d, want 1", count)
	}
}
