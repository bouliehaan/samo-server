package users

import (
	"context"
	"errors"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestManageAccounts(t *testing.T) {
	ctx := context.Background()
	s := New(ServiceOptions{DB: storagetest.Open(t)})
	if err := s.Bootstrap(ctx, BootstrapInput{AdminUsername: "owner", AdminPassword: "owner-password"}); err != nil {
		t.Fatal(err)
	}
	admin, err := s.AuthenticateCredentials(ctx, "owner", "owner-password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.Create(ctx, admin, CreateUserInput{Username: "listener", Password: "old-password"})
	if err != nil {
		t.Fatal(err)
	}
	listener := Principal{User: user}
	role := RoleAdmin
	if _, err := s.Update(ctx, listener, user.ID, UpdateUserInput{Role: &role}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("self elevation: %v", err)
	}
	if err := s.Delete(ctx, listener, admin.User.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("listener delete: %v", err)
	}
	if err := s.Delete(ctx, admin, admin.User.ID); !errors.Is(err, ErrProtectedUser) {
		t.Fatalf("self delete: %v", err)
	}
	if err := s.Delete(ctx, admin, BootstrapUserID); !errors.Is(err, ErrProtectedUser) {
		t.Fatalf("reserved delete: %v", err)
	}
	demote := RoleUser
	if _, err := s.Update(ctx, admin, admin.User.ID, UpdateUserInput{Role: &demote}); !errors.Is(err, ErrProtectedUser) {
		t.Fatalf("self demote: %v", err)
	}
	username, display := "renamed", "New Name"
	updated, err := s.Update(ctx, admin, user.ID, UpdateUserInput{Username: &username, DisplayName: &display, Role: &role})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Username != username || updated.DisplayName != display || updated.Role != role {
		t.Fatalf("update: %+v", updated)
	}
	token, err := s.IssueToken(ctx, listener, CreateTokenInput{})
	if err != nil {
		t.Fatal(err)
	}
	stream, _, err := s.IssueStreamToken(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GenerateSubsonicPassword(ctx, listener); err != nil {
		t.Fatal(err)
	}
	password := "new-password"
	if _, err := s.Update(ctx, admin, user.ID, UpdateUserInput{Password: &password}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateCredentials(ctx, username, "old-password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old password: %v", err)
	}
	if _, err := s.AuthenticateCredentials(ctx, username, password); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateToken(ctx, token.Secret); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old bearer: %v", err)
	}
	if _, err := s.AuthenticateStreamToken(ctx, stream); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old stream: %v", err)
	}
	if enabled, err := s.SubsonicEnabled(ctx, user.ID); err != nil || enabled {
		t.Fatalf("subsonic: %v %v", enabled, err)
	}
	if _, err := s.Update(ctx, admin, user.ID, UpdateUserInput{Role: &demote}); err != nil {
		t.Fatal(err)
	}
	// Recheck current database role, even if a request carries a stale principal.
	if _, err := s.Update(ctx, Principal{User: updated}, admin.User.ID, UpdateUserInput{DisplayName: &display}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale admin: %v", err)
	}
	token, err = s.IssueToken(ctx, listener, CreateTokenInput{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, admin, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, user.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted user: %v", err)
	}
	if _, err := s.AuthenticateToken(ctx, token.Secret); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted bearer: %v", err)
	}
}

func TestAccountUpdateValidationAndLastAdmin(t *testing.T) {
	ctx := context.Background()
	s := New(ServiceOptions{DB: storagetest.Open(t)})
	s.Bootstrap(ctx, BootstrapInput{AdminUsername: "owner", AdminPassword: "password"})
	admin, _ := s.AuthenticateCredentials(ctx, "owner", "password")
	user, err := s.Create(ctx, admin, CreateUserInput{Username: "listener", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input UpdateUserInput
		want  error
	}{
		{UpdateUserInput{Username: ptr("invalid name")}, ErrInvalidUsername},
		{UpdateUserInput{Username: ptr("owner")}, ErrUsernameTaken},
		{UpdateUserInput{Role: ptr("superuser")}, ErrInvalidRole},
		{UpdateUserInput{Password: ptr("")}, ErrInvalidPassword},
	} {
		if _, err := s.Update(ctx, admin, user.ID, tc.input); !errors.Is(err, tc.want) {
			t.Fatalf("got %v want %v", err, tc.want)
		}
	}
	server, _ := s.Get(ctx, BootstrapUserID)
	if err := s.Delete(ctx, Principal{User: server}, admin.User.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin: %v", err)
	}
	if _, err := s.Update(ctx, admin, "missing", UpdateUserInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
}
func ptr(s string) *string { return &s }

func TestConcurrentAdminDeletionKeepsAnAdministrator(t *testing.T) {
	ctx := context.Background()
	s := New(ServiceOptions{DB: storagetest.Open(t)})
	if err := s.Bootstrap(ctx, BootstrapInput{AdminUsername: "owner", AdminPassword: "password"}); err != nil {
		t.Fatal(err)
	}
	first, err := s.AuthenticateCredentials(ctx, "owner", "password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(ctx, first, CreateUserInput{Username: "second", Password: "password", Role: RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- s.Delete(ctx, first, second.ID) }()
	go func() { <-start; results <- s.Delete(ctx, Principal{User: second}, first.User.ID) }()
	close(start)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrLastAdmin) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful deletions: %d", successes)
	}
	items, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admins := 0
	for _, user := range items {
		if user.Role == RoleAdmin && user.ID != BootstrapUserID {
			admins++
		}
	}
	if admins != 1 {
		t.Fatalf("remaining administrators: %d", admins)
	}
}
