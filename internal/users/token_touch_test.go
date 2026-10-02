package users

import (
	"context"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// Production authenticates through a pool pinned read-only, so recording a
// token's use has to go through the write pool. It used to run on the read
// pool, where Postgres refused the UPDATE on every request, the error was
// dropped, and last_used_at never moved.
func TestAuthenticateTokenRecordsUseThroughReadOnlyPool(t *testing.T) {
	ctx := context.Background()
	db, dsn := storagetest.OpenWithDSN(t)
	readDB, err := storage.OpenReadOnly(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { readDB.Close() })
	if _, err := readDB.ExecContext(ctx, `UPDATE user_tokens SET last_used_at = NULL`); err == nil {
		t.Fatal("read pool accepted a write, so this test proves nothing")
	}

	s := New(ServiceOptions{DB: db, ReadDB: readDB})
	if err := s.Bootstrap(ctx, BootstrapInput{AdminUsername: "owner", AdminPassword: "owner-password"}); err != nil {
		t.Fatal(err)
	}
	admin, err := s.AuthenticateCredentials(ctx, "owner", "owner-password")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := s.IssueToken(ctx, admin, CreateTokenInput{Label: "client"})
	if err != nil {
		t.Fatal(err)
	}

	lastUsed := func() time.Time {
		t.Helper()
		tokens, err := listTokens(ctx, db, admin.User.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range tokens {
			if token.ID == issued.Token.ID {
				if token.LastUsedAt == nil {
					return time.Time{}
				}
				return *token.LastUsedAt
			}
		}
		t.Fatalf("token %s missing", issued.Token.ID)
		return time.Time{}
	}
	setLastUsed := func(at time.Time) {
		t.Helper()
		if err := touchToken(ctx, db, issued.Token.ID, at); err != nil {
			t.Fatal(err)
		}
	}
	authenticate := func() {
		t.Helper()
		if _, err := s.AuthenticateToken(ctx, issued.Secret); err != nil {
			t.Fatal(err)
		}
		s.touches.Wait()
	}

	if got := lastUsed(); !got.IsZero() {
		t.Fatalf("fresh token already has last_used_at %v", got)
	}
	before := time.Now().UTC().Truncate(time.Second)
	authenticate()
	if got := lastUsed(); got.Before(before) {
		t.Fatalf("first use: last_used_at = %v, want >= %v", got, before)
	}

	// Stored use within the interval: no write.
	s.touchedAt = nil
	recent := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Second)
	setLastUsed(recent)
	authenticate()
	if got := lastUsed(); !got.Equal(recent) {
		t.Fatalf("use 30s after the last: last_used_at = %v, want it left at %v", got, recent)
	}

	// Stored use gone stale: rewritten.
	stale := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	setLastUsed(stale)
	before = time.Now().UTC().Truncate(time.Second)
	authenticate()
	if got := lastUsed(); got.Before(before) {
		t.Fatalf("use after a stale one: last_used_at = %v, want >= %v", got, before)
	}

	// Stale again, but a write for this token went out under a minute ago
	// (as if it were still queued behind a busy write pool): no second write.
	setLastUsed(stale)
	authenticate()
	if got := lastUsed(); !got.Equal(stale) {
		t.Fatalf("use while a write is already dispatched: last_used_at = %v, want it left at %v", got, stale)
	}
}
