package storage

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/migrations"
)

// TestConcurrentWritesDoNotBusy hammers the read-write pool with many goroutines
// writing simultaneously and asserts none sees SQLITE_BUSY. It passes on the
// normal multi-connection pool because busy_timeout absorbs plain write
// contention — which is the key finding: ordinary concurrency does NOT produce
// the production BUSY. That came from a write lock held longer than the timeout
// (a long-running operation), so the real fix is finding that holder, not
// shrinking the pool (a single-connection pool deadlocks this codebase's boot).
func TestConcurrentWritesDoNotBusy(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `CREATE TABLE stress (id INTEGER PRIMARY KEY AUTOINCREMENT, v TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	const writers, perWriter = 24, 200
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := db.ExecContext(ctx, `INSERT INTO stress (v) VALUES (?)`, fmt.Sprintf("w%d-%d", w, i)); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent writes deadlocked / did not finish within 30s")
	}
	close(errs)

	for err := range errs {
		if IsBusy(err) {
			t.Fatalf("SQLITE_BUSY under the single-writer pool — the fix is not holding: %v", err)
		}
		t.Fatalf("unexpected write error: %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stress`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if want := writers * perWriter; n != want {
		t.Fatalf("row count = %d, want %d (writes were lost)", n, want)
	}
}

func TestApplyMigrationsIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = '001_init.sql'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration count = %d, want 1", count)
	}
}
