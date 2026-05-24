package libraries

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		Name: "Books",
		Kind: KindAudiobook,
		Path: booksDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Kind != KindAudiobook {
		t.Fatalf("kind = %q, want audiobook", created.Kind)
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
	// Scans are async — the call returns a "running" job; wait for the
	// goroutine to finish so we can verify the final state.
	final := waitForScanJob(t, ctx, service, result.Job.ID)
	if final.Status != ScanStatusCompleted {
		t.Fatalf("status = %q, want completed (error=%q)", final.Status, final.Error)
	}
	if final.LibraryID != created.ID {
		t.Fatalf("library id = %q, want %q", final.LibraryID, created.ID)
	}

	jobs, err := service.ListScanJobs(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if jobs.Total == 0 {
		t.Fatal("expected scan jobs to be recorded")
	}
}

// waitForScanJob polls a scan job until it leaves the running state,
// matching how the dashboard observes async scans in production.
func waitForScanJob(t *testing.T, ctx context.Context, service *Service, jobID string) ScanJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		job, err := service.GetScanJob(ctx, jobID)
		if err != nil {
			t.Fatalf("get scan job %q: %v", jobID, err)
		}
		if job.Status != ScanStatusRunning && job.Status != ScanStatusPending {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan job %q stuck in %q after 10s", jobID, job.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
