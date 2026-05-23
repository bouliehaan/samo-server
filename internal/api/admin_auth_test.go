package api

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/sources"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/users"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestAdminOnlyRoutesRejectNormalUsers(t *testing.T) {
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

	userService, adminToken, userToken := testUserServiceWithTokens(t, ctx, db)
	handler := NewServer(ServerOptions{
		Libraries:     libraries.New(db, scanner.New(db)),
		MetadataApply: metadata.NewMetadataApplyService(db),
		Sources:       sources.New(db),
		Users:         userService,
	})

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "list libraries", method: http.MethodGet, path: "/api/v1/libraries"},
		{name: "create library", method: http.MethodPost, path: "/api/v1/libraries", body: `{}`},
		{name: "create podcast feed", method: http.MethodPost, path: "/api/v1/shelf/podcast-feeds", body: `{}`},
		{name: "apply metadata", method: http.MethodPost, path: "/api/v1/metadata/apply", body: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Authorization", "Bearer "+userToken)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}

	libraryDir := filepath.Join(root, "music")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/libraries", bytes.NewBufferString(`{
		"name": "Music",
		"kind": "music",
		"path": "`+libraryDir+`"
	}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin create status = %d, want %d body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func testUserServiceWithTokens(t *testing.T, ctx context.Context, db *sql.DB) (*users.Service, string, string) {
	t.Helper()
	service := users.New(users.ServiceOptions{DB: db})
	if err := service.Bootstrap(ctx, users.BootstrapInput{
		AdminUsername: "admin",
		AdminPassword: "admin-pass",
	}); err != nil {
		t.Fatal(err)
	}
	admin, err := service.AuthenticateCredentials(ctx, "admin", "admin-pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, admin, users.CreateUserInput{
		Username: "listener",
		Password: "listener-pass",
		Role:     users.RoleUser,
	}); err != nil {
		t.Fatal(err)
	}
	listener, err := service.AuthenticateCredentials(ctx, "listener", "listener-pass")
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := service.IssueToken(ctx, admin, users.CreateTokenInput{Label: "admin test"})
	if err != nil {
		t.Fatal(err)
	}
	userToken, err := service.IssueToken(ctx, listener, users.CreateTokenInput{Label: "user test"})
	if err != nil {
		t.Fatal(err)
	}
	return service, adminToken.Secret, userToken.Secret
}
