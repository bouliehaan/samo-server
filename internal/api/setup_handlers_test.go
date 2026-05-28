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
	"github.com/bouliehaan/samo-server/internal/users"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestSetupWizardFlow(t *testing.T) {
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

	userService := users.New(users.ServiceOptions{DB: db})
	libraryService := libraries.New(db, scanner.New(db))
	handler := NewServer(ServerOptions{
		Libraries: libraryService,
		Users:     userService,
	})

	// Step 1: status reports needsSetup before admin exists.
	rec := doRequest(handler, http.MethodGet, "/api/v1/setup/status", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var status SetupStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.NeedsSetup || status.HasAdmin || status.CurrentStep != setupStepAdmin {
		t.Fatalf("initial status = %+v", status)
	}

	// Step 2: create the first admin and capture the token.
	body := `{"username": "admin", "password": "samo-rocks-12345"}`
	rec = doRequest(handler, http.MethodPost, "/api/v1/setup/admin", body, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create admin status = %d body=%s", rec.Code, rec.Body.String())
	}
	var login users.LoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if login.Token == "" {
		t.Fatalf("login.Token is empty")
	}
	adminToken := login.Token

	// Status should now advance to libraries.
	rec = doRequest(handler, http.MethodGet, "/api/v1/setup/status", "", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.HasAdmin {
		t.Fatalf("HasAdmin = false after admin creation; status=%+v", status)
	}
	if status.CurrentStep != setupStepLibraries {
		t.Fatalf("CurrentStep = %q, want %q", status.CurrentStep, setupStepLibraries)
	}

	// Step 3: re-creating the admin should be rejected now.
	rec = doRequest(handler, http.MethodPost, "/api/v1/setup/admin", body, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("repeat admin creation status = %d, want %d body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	// Step 4: add a library with the admin token.
	libDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	libBody := `{"name": "Music", "kind": "music", "path": "` + libDir + `"}`
	rec = doRequest(handler, http.MethodPost, "/api/v1/setup/libraries", libBody, adminToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create library status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Library creation without the token must fail.
	rec = doRequest(handler, http.MethodPost, "/api/v1/setup/libraries", libBody, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token library status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// Status reports libraries done.
	rec = doRequest(handler, http.MethodGet, "/api/v1/setup/status", "", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.HasLibrary || status.LibraryCount == 0 {
		t.Fatalf("expected HasLibrary after creation; status=%+v", status)
	}
}

func TestSetupDirectoryBrowserRootEntries(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	handler := NewServer(ServerOptions{
		Libraries: libraries.New(db, scanner.New(db)),
		Users:     users.New(users.ServiceOptions{DB: db}),
	})

	rec := doRequest(handler, http.MethodGet, "/api/v1/setup/directories", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("directories status = %d body=%s", rec.Code, rec.Body.String())
	}
	var listing setupDirectoryListing
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Path != "" {
		t.Fatalf("root listing should not carry a path, got %q", listing.Path)
	}
}

func TestSetupRejectsSystemPaths(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{
		Libraries: libraries.New(db, scanner.New(db)),
		Users:     users.New(users.ServiceOptions{DB: db}),
	})

	rec := doRequest(handler, http.MethodGet, "/api/v1/setup/directories?path=/proc", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status for /proc = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSetupPageRedirectsAfterCompletion(t *testing.T) {
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

	userService := users.New(users.ServiceOptions{DB: db})
	if err := userService.Bootstrap(ctx, users.BootstrapInput{AdminUsername: "admin", AdminPassword: "samo-rocks-12345"}); err != nil {
		t.Fatal(err)
	}
	libraryService := libraries.New(db, scanner.New(db))
	libDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := libraryService.Create(ctx, libraries.CreateLibraryInput{
		Name: "Music",
		Kind: "music",
		Path: libDir,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := libraryService.ScanAll(ctx, libraries.TriggerStartup, ""); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{
		Libraries: libraryService,
		Users:     userService,
	})

	rec := doRequest(handler, http.MethodGet, "/setup", "", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect from /setup once complete; got %d", rec.Code)
	}
	if location := rec.Header().Get("Location"); location != "/" {
		t.Fatalf("redirect location = %q, want /", location)
	}
}

func doRequest(handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	var reader *bytes.Buffer
	if body != "" {
		reader = bytes.NewBufferString(body)
	} else {
		reader = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
