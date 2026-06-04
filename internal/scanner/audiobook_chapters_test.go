package scanner

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestPersistAudiobookGroupClearsStaleChaptersWhenProbeEmpty(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	s := New(db)
	lib := Library{ID: "lib-books", Name: "Books", Kind: "audiobook", Path: "/books"}
	if err := s.upsertLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}

	group := groupedAudio{
		Root:  "/books/Author/Title",
		Files: []string{"/books/Author/Title/book.m4b"},
	}
	bookID := stableID("audiobook", lib.ID, group.Root)
	if _, err := s.upsertAudiobook(ctx, catalog.AudiobookItem{
		ID:        bookID,
		LibraryID: lib.ID,
		Path:      group.Root,
		Book:      &catalog.BookMetadata{Title: "Title"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO audiobook_chapters (id, audiobook_id, chapter_index, title, start_seconds, end_seconds)
		VALUES ('chapter-1', ?, 1, 'Existing Chapter', 0, 120)`, bookID); err != nil {
		t.Fatal(err)
	}

	// Full scan semantics: unchanged-file shortcuts disabled.
	s.scanMode = ScanModeFull
	if err := s.persistAudiobookGroup(ctx, lib, lib.Path, group, nil); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audiobook_chapters WHERE audiobook_id = ?`, bookID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("chapter rows = %d, want 0 (stale markers cleared)", count)
	}
}

func TestFlattenBookChaptersUsesPositiveFallbackDuration(t *testing.T) {
	chapters := flattenBookChapters([]probedFile{{
		AudioFile: catalog.AudioFile{
			Path:            "/books/title.m4b",
			DurationSeconds: 0,
		},
		Tags: catalog.Tags{"title": []string{"Title"}},
	}})
	if len(chapters) != 1 {
		t.Fatalf("chapters = %d, want 1", len(chapters))
	}
	if chapters[0].EndSeconds <= chapters[0].StartSeconds {
		t.Fatalf("chapter range invalid: %v-%v", chapters[0].StartSeconds, chapters[0].EndSeconds)
	}
}
