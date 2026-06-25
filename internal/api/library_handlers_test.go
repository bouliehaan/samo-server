package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestLibraryScanAPI(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	libraryDir := filepath.Join(root, "audiobooks")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
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

	libraryService := libraries.New(db, scanner.New(db))
	handler := NewServer(ServerOptions{Libraries: libraryService})

	body, _ := json.Marshal(libraries.CreateLibraryInput{
		Name: "Audiobooks",
		Kind: libraries.KindAudiobook,
		Path: libraryDir,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/libraries", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var created libraries.Library
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/libraries/"+created.ID+"/scan", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	// Async contract: POST returns 202 with a "running" job; the dashboard
	// polls /api/v1/scan/jobs/{id} for completion.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("scan status = %d, want %d body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	var result libraries.ScanResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Job.Status != libraries.ScanStatusRunning {
		t.Fatalf("initial job status = %q, want running", result.Job.Status)
	}

	// Poll the job until the goroutine settles.
	deadline := time.Now().Add(10 * time.Second)
	var final libraries.ScanJob
	for {
		job, err := libraryService.GetScanJob(ctx, result.Job.ID)
		if err != nil {
			t.Fatalf("get scan job: %v", err)
		}
		if job.Status != libraries.ScanStatusRunning && job.Status != libraries.ScanStatusPending {
			final = job
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan job %q stuck in %q after 10s", result.Job.ID, job.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.Status != libraries.ScanStatusCompleted {
		t.Fatalf("final status = %q, want completed (error=%q)", final.Status, final.Error)
	}
}
