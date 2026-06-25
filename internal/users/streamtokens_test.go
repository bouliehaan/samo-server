package users

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestStreamTokenRoundTrip(t *testing.T) {
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
	if _, err := service.BootstrapWithResult(ctx, BootstrapInput{AdminUsername: "owner", AdminPassword: "samo-stream-1234"}); err != nil {
		t.Fatal(err)
	}
	owner, err := service.GetByUsername(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}

	token, expiresAt, err := service.IssueStreamToken(owner.ID)
	if err != nil {
		t.Fatalf("IssueStreamToken: %v", err)
	}
	if token == "" {
		t.Fatal("token empty")
	}
	if !expiresAt.After(time.Now()) {
		t.Fatalf("expiresAt %v is not in the future", expiresAt)
	}

	principal, err := service.AuthenticateStreamToken(ctx, token)
	if err != nil {
		t.Fatalf("AuthenticateStreamToken: %v", err)
	}
	if principal.User.ID != owner.ID {
		t.Fatalf("principal user = %q, want %q", principal.User.ID, owner.ID)
	}

	// Garbage token rejected.
	if _, err := service.AuthenticateStreamToken(ctx, "smt_not_real"); err == nil {
		t.Fatal("expected unauthorized for unknown token")
	}
}

func TestStreamTokenRejectsExpired(t *testing.T) {
	store := newStreamTokenStore()
	token, _, err := store.issue("user-test")
	if err != nil {
		t.Fatal(err)
	}
	// Force-expire the entry.
	store.mu.Lock()
	entry := store.tokens[token]
	entry.expiresAt = time.Now().Add(-time.Minute)
	store.tokens[token] = entry
	store.mu.Unlock()

	if _, ok := store.validate(token); ok {
		t.Fatal("expected validate to reject expired token")
	}
	// And the entry should be GC'd on the next read.
	store.mu.Lock()
	_, stillThere := store.tokens[token]
	store.mu.Unlock()
	if stillThere {
		t.Fatal("expired token was not garbage-collected")
	}
}
