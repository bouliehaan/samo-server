package users

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestBootstrapDefersAdminWhenNoEnvVars(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	// First boot with no env vars: bootstrap leaves admin creation to the
	// /setup wizard rather than auto-generating a password and logging it.
	service := New(ServiceOptions{DB: db})
	result, err := service.BootstrapWithResult(ctx, BootstrapInput{})
	if err != nil {
		t.Fatal(err)
	}
	if result.CreatedAdmin {
		t.Fatal("CreatedAdmin = true, want false (defer to /setup)")
	}
	if result.GeneratedPassword != "" {
		t.Fatalf("GeneratedPassword = %q, want empty", result.GeneratedPassword)
	}
}

func TestBootstrapGeneratesPasswordWhenUsernameSet(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(ServiceOptions{DB: db})
	result, err := service.BootstrapWithResult(ctx, BootstrapInput{AdminUsername: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.CreatedAdmin {
		t.Fatal("CreatedAdmin = false, want true when username is set")
	}
	if result.GeneratedPassword == "" {
		t.Fatal("GeneratedPassword empty")
	}
	if _, err := service.AuthenticateCredentials(ctx, "owner", result.GeneratedPassword); err != nil {
		t.Fatalf("auth with generated password failed: %v", err)
	}
}

func TestBootstrapExplicitPasswordUpdatesExistingAdmin(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(ServiceOptions{DB: db})
	if _, err := service.BootstrapWithResult(ctx, BootstrapInput{
		AdminUsername: "admin",
		AdminPassword: "old-pass",
	}); err != nil {
		t.Fatal(err)
	}
	result, err := service.BootstrapWithResult(ctx, BootstrapInput{
		AdminUsername: "admin",
		AdminPassword: "new-pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.UpdatedPassword {
		t.Fatal("UpdatedPassword = false, want true")
	}
	if _, err := service.AuthenticateCredentials(ctx, "admin", "new-pass"); err != nil {
		t.Fatalf("new password auth failed: %v", err)
	}
	if _, err := service.AuthenticateCredentials(ctx, "admin", "old-pass"); err != ErrUnauthorized {
		t.Fatalf("old password auth error = %v, want unauthorized", err)
	}
}
