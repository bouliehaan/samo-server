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
		Name:      "Audiobooks",
		Kind:      libraries.KindShelf,
		MediaType: libraries.MediaTypeBook,
		Path:      libraryDir,
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
	if rec.Code != http.StatusOK {
		t.Fatalf("scan status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var result libraries.ScanResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Job.Status != libraries.ScanStatusCompleted {
		t.Fatalf("job status = %q, want completed", result.Job.Status)
	}
}
