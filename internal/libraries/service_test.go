package libraries

import (
	"context"
	"errors"
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

	result, err := service.ScanLibrary(ctx, created.ID, TriggerAPI, "")
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

func TestScanLibraryRejectsDifferentActiveJob(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	musicDir := filepath.Join(root, "music")
	booksDir := filepath.Join(root, "books")
	for _, dir := range []string{musicDir, booksDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
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
	music, err := service.Create(ctx, CreateLibraryInput{Name: "Music", Kind: KindMusic, Path: musicDir})
	if err != nil {
		t.Fatal(err)
	}
	books, err := service.Create(ctx, CreateLibraryInput{Name: "Books", Kind: KindAudiobook, Path: booksDir})
	if err != nil {
		t.Fatal(err)
	}

	all, err := service.ScanAll(ctx, TriggerAPI, "")
	if err != nil {
		t.Fatal(err)
	}
	if all.Job.ID == "" {
		t.Fatal("expected scan-all job id")
	}

	_, err = service.ScanLibrary(ctx, books.ID, TriggerAPI, "")
	if !errors.Is(err, ErrScanInProgress) {
		t.Fatalf("err = %v, want ErrScanInProgress", err)
	}

	same, err := service.ScanAll(ctx, TriggerAPI, "")
	if err != nil {
		t.Fatal(err)
	}
	if same.Job.ID != all.Job.ID {
		t.Fatalf("job id = %q, want %q", same.Job.ID, all.Job.ID)
	}

	_ = music
	waitForScanJob(t, ctx, service, all.Job.ID)
}

func TestCancelScanRejectsFinishedJob(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db, scanner.New(db))
	job := ScanJob{
		ID:            "scan_done",
		Status:        ScanStatusCompleted,
		Scope:         ScanScopeAll,
		TriggerSource: TriggerAPI,
		ScanMode:      ScanModeFull,
		StartedAt:     time.Now().UTC(),
	}
	if err := insertScanJob(ctx, db, job); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelScan(ctx, job.ID); !errors.Is(err, ErrScanNotCancellable) {
		t.Fatalf("CancelScan err = %v, want ErrScanNotCancellable", err)
	}
	if _, err := service.CancelActiveScan(ctx); !errors.Is(err, ErrScanNotCancellable) {
		t.Fatalf("CancelActiveScan err = %v, want ErrScanNotCancellable", err)
	}
}

func TestReconcileOrphanScansClosesStaleRunningRows(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db, scanner.New(db))

	stale := ScanJob{
		ID:            "scan_orphan",
		Status:        ScanStatusRunning,
		Scope:         ScanScopeAll,
		TriggerSource: TriggerAPI,
		ScanMode:      ScanModeFull,
		StartedAt:     time.Now().UTC().Add(-time.Hour),
	}
	if err := insertScanJob(ctx, db, stale); err != nil {
		t.Fatal(err)
	}
	finished := ScanJob{
		ID:            "scan_finished",
		Status:        ScanStatusCompleted,
		Scope:         ScanScopeAll,
		TriggerSource: TriggerAPI,
		ScanMode:      ScanModeFull,
		StartedAt:     time.Now().UTC().Add(-2 * time.Hour),
	}
	if err := insertScanJob(ctx, db, finished); err != nil {
		t.Fatal(err)
	}

	count, err := service.ReconcileOrphanScans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("reconciled = %d, want 1", count)
	}

	reconciled, err := service.GetScanJob(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Status != ScanStatusFailed {
		t.Fatalf("orphan status = %q, want %q", reconciled.Status, ScanStatusFailed)
	}
	if reconciled.FinishedAt == nil {
		t.Fatal("orphan finished_at should be set")
	}
	if reconciled.Error == "" {
		t.Fatal("orphan error should be set so the operator knows why")
	}

	untouched, err := service.GetScanJob(ctx, finished.ID)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Status != ScanStatusCompleted {
		t.Fatalf("completed job touched: status = %q", untouched.Status)
	}
}

func TestCancelScanClearsOrphanRunningRow(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db, scanner.New(db))
	orphan := ScanJob{
		ID:            "scan_orphan",
		Status:        ScanStatusRunning,
		Scope:         ScanScopeAll,
		TriggerSource: TriggerAPI,
		ScanMode:      ScanModeFull,
		StartedAt:     time.Now().UTC().Add(-time.Hour),
	}
	if err := insertScanJob(ctx, db, orphan); err != nil {
		t.Fatal(err)
	}

	// No goroutine owns this job (activeJobID is empty), so CancelScan
	// used to return ErrScanNotCancellable and the operator was stuck
	// with a ghost "running" row. Now it should mark the row cancelled.
	got, err := service.CancelScan(ctx, orphan.ID)
	if err != nil {
		t.Fatalf("CancelScan err = %v, want nil", err)
	}
	if got.Status != ScanStatusCancelled {
		t.Fatalf("status = %q, want %q", got.Status, ScanStatusCancelled)
	}
	if got.FinishedAt == nil {
		t.Fatal("finished_at should be set after cancel")
	}

	persisted, err := service.GetScanJob(ctx, orphan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != ScanStatusCancelled {
		t.Fatalf("persisted status = %q, want %q", persisted.Status, ScanStatusCancelled)
	}
}

func TestFinishScanJobPersistsOnCancelledContext(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db, scanner.New(db))
	job := ScanJob{
		ID:            "scan_cancel_persist",
		Status:        ScanStatusRunning,
		Scope:         ScanScopeAll,
		TriggerSource: TriggerAPI,
		ScanMode:      ScanModeFull,
		StartedAt:     time.Now().UTC(),
		FilesSeen:     42,
	}
	if err := insertScanJob(ctx, db, job); err != nil {
		t.Fatal(err)
	}

	scanCtx, cancel := context.WithCancel(ctx)
	cancel()

	service.finishScanJob(scanCtx, &job, scanner.ScanStats{FilesSeen: 42}, context.Canceled)

	persisted, err := getScanJob(ctx, db, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != ScanStatusCancelled {
		t.Fatalf("status = %q, want %q", persisted.Status, ScanStatusCancelled)
	}
	if persisted.FinishedAt == nil {
		t.Fatal("finished_at should be set")
	}
	if persisted.FilesSeen != 42 {
		t.Fatalf("files_seen = %d, want 42", persisted.FilesSeen)
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
