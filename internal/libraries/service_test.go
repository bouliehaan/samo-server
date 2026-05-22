package libraries

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestLibraryCRUDAndScanJob(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	musicDir := filepath.Join(root, "music")
	if err := os.MkdirAll(musicDir, 0o755); err != nil {
		t.Fatal(err)
	}

	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db, scanner.New(db))
	if err := service.SyncConfigured(ctx, []config.Library{{
		Name: "Music",
		Kind: KindMusic,
		Path: musicDir,
	}}); err != nil {
		t.Fatal(err)
	}

	booksDir := filepath.Join(root, "books")
	if err := os.MkdirAll(booksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, CreateLibraryInput{
		Name:      "Books",
		Kind:      KindShelf,
		MediaType: MediaTypeBook,
		Path:      booksDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Kind != KindShelf {
		t.Fatalf("kind = %q, want shelf", created.Kind)
	}

	page, err := service.List(ctx, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total < 2 {
		t.Fatalf("total = %d, want at least 2 libraries", page.Total)
	}

	result, err := service.ScanLibrary(ctx, created.ID, TriggerAPI)
	if err != nil {
		t.Fatal(err)
	}
	if result.Job.Status != ScanStatusCompleted {
		t.Fatalf("status = %q, want completed", result.Job.Status)
	}
	if result.Job.LibraryID != created.ID {
		t.Fatalf("library id = %q, want %q", result.Job.LibraryID, created.ID)
	}

	jobs, err := service.ListScanJobs(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if jobs.Total == 0 {
		t.Fatal("expected scan jobs to be recorded")
	}
}
