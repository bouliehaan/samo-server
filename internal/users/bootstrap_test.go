package users

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestBootstrapDefersAdminWhenNoEnvVars(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

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
	db := storagetest.Open(t)

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

// The row ensureServerToken writes follows SAMO_API_TOKEN: a changed secret
// replaces the old one, and a removed one stops authenticating at the next
// start. The reserved account's other tokens are not the shared token and
// survive both.
func TestBootstrapKeepsTheSharedTokenInStepWithTheVariable(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	start := func(legacy string) (*Service, BootstrapResult) {
		t.Helper()
		service := New(ServiceOptions{DB: db, LegacyAPIToken: legacy})
		result, err := service.BootstrapWithResult(ctx, BootstrapInput{})
		if err != nil {
			t.Fatal(err)
		}
		return service, result
	}

	service, _ := start("shared-one")
	device, err := service.IssueToken(ctx, Principal{User: User{ID: BootstrapUserID}}, CreateTokenInput{Label: "samo-radio: kitchen"})
	if err != nil {
		t.Fatal(err)
	}

	service, _ = start("shared-two")
	if _, err := service.AuthenticateToken(ctx, "shared-one"); err != ErrUnauthorized {
		t.Fatalf("rotated-out secret: err = %v, want unauthorized", err)
	}

	service, result := start("")
	if !result.RetiredServerToken {
		t.Fatal("RetiredServerToken = false after the variable was removed")
	}
	for _, secret := range []string{"shared-one", "shared-two"} {
		if _, err := service.AuthenticateToken(ctx, secret); err != ErrUnauthorized {
			t.Fatalf("%s after removal: err = %v, want unauthorized", secret, err)
		}
	}
	if _, err := service.AuthenticateToken(ctx, device.Secret); err != nil {
		t.Fatalf("a device token on the reserved account was retired with it: %v", err)
	}

	if _, result := start(""); result.RetiredServerToken {
		t.Fatal("RetiredServerToken = true with nothing left to retire")
	}
}

func TestBootstrapExplicitPasswordUpdatesExistingAdmin(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

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
