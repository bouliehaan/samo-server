package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/migrations"
)

// TestConcurrentWritesSucceed hammers the pool with many goroutines writing
// simultaneously. Postgres has real row-level concurrency, so unlike the
// SQLite era there is no BUSY class of error to absorb — every write must
// simply succeed, and none may be lost.
func TestConcurrentWritesSucceed(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	if _, err := db.ExecContext(ctx, `CREATE TABLE stress (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
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
	case <-time.After(60 * time.Second):
		t.Fatal("concurrent writes did not finish within 60s")
	}
	close(errs)

	for err := range errs {
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
	db := storagetest.Open(t) // already migrated via the template

	// Re-applying the full lineage must be a no-op.
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = '0001_init.sql'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration count = %d, want 1", count)
	}
}

// TestOpenReadOnlyRejectsWrites proves the read pool's server-side pin: a
// write through the handle main.go hands to read-only consumers must fail
// loudly instead of mutating state.
func TestOpenReadOnlyRejectsWrites(t *testing.T) {
	ctx := context.Background()
	_, dsn := storagetest.OpenWithDSN(t)

	readDB, err := storage.OpenReadOnly(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer readDB.Close()

	var n int
	if err := readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries`).Scan(&n); err != nil {
		t.Fatalf("read through read-only pool failed: %v", err)
	}

	_, err = readDB.ExecContext(ctx,
		`INSERT INTO libraries (id, name, kind, path) VALUES (?, ?, ?, ?)`,
		"lib-ro", "Nope", "music", "/nope")
	if err == nil {
		t.Fatal("write through the read-only pool unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected a read-only transaction error, got: %v", err)
	}
}

// TestApplyMigrationsIsConcurrencySafe proves the advisory lock in
// ApplyMigrations serializes concurrent callers — the case that happens when
// the server and a scan subprocess (or two racing restarts) migrate the same
// database at once. The probe migration's DDL is deliberately NOT idempotent
// on its own: without the lock, two callers would both find it unapplied and
// the second CREATE TABLE would fail. With the lock, exactly one applies it.
func TestApplyMigrationsIsConcurrencySafe(t *testing.T) {
	ctx := context.Background()
	// Use a full-width pool (not storagetest's 8) so many callers can each hold
	// a lock connection while the winner still gets a connection to do the work.
	_, dsn := storagetest.OpenWithDSN(t)
	db, err := storage.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// A single INSERT row is the witness: if the body ran more than once the
	// count would exceed 1 (or the second run would have errored outright).
	probe := fstest.MapFS{
		"0001_probe.sql": &fstest.MapFile{Data: []byte(
			"CREATE TABLE probe_concurrency (id BIGINT PRIMARY KEY);\n" +
				"INSERT INTO probe_concurrency (id) VALUES (1);")},
	}

	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- storage.ApplyMigrations(ctx, db, probe)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ApplyMigrations returned error: %v", err)
		}
	}

	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_concurrency`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("probe_concurrency rows = %d, want 1 (migration body applied more than once)", rows)
	}
	var ledger int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = '0001_probe.sql'`).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if ledger != 1 {
		t.Fatalf("schema_migrations rows for probe = %d, want 1", ledger)
	}
}

// TestWithRetryTx covers the transaction contract: commit on success, rollback
// on error, and immediate propagation of a non-retryable (domain) error.
func TestWithRetryTx(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE tx_probe (id BIGINT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	t.Run("commits_on_success", func(t *testing.T) {
		err := storage.WithRetryTx(ctx, db, 3, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO tx_probe (id, v) VALUES (?, ?)`, 1, "ok")
			return err
		})
		if err != nil {
			t.Fatalf("WithRetryTx returned error: %v", err)
		}
		var v string
		if err := db.QueryRowContext(ctx, `SELECT v FROM tx_probe WHERE id = ?`, 1).Scan(&v); err != nil {
			t.Fatalf("committed row missing: %v", err)
		}
		if v != "ok" {
			t.Fatalf("v = %q, want ok", v)
		}
	})

	t.Run("rolls_back_on_error", func(t *testing.T) {
		sentinel := errors.New("boom")
		err := storage.WithRetryTx(ctx, db, 3, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO tx_probe (id, v) VALUES (?, ?)`, 2, "doomed"); err != nil {
				return err
			}
			return sentinel // fn fails after a write: the write must not persist
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel error, got: %v", err)
		}
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tx_probe WHERE id = ?`, 2).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("rolled-back row still present: count = %d", n)
		}
	})
}

// TestSchemaHardening asserts the robustness fixes in migration 0006 are live:
// the four foreign-key cascade indexes exist, and json_extract tolerates an
// empty string instead of raising (which would fail a whole scan-match query).
func TestSchemaHardening(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	t.Run("fk_cascade_indexes_exist", func(t *testing.T) {
		for _, name := range []string{
			"idx_bookmarks_audiobook",
			"idx_collection_audiobooks_audiobook",
			"idx_channel_schedule_rules_source",
			"idx_podcast_episodes_library",
		} {
			var got string
			if err := db.QueryRowContext(ctx,
				`SELECT indexname FROM pg_indexes WHERE indexname = ?`, name).Scan(&got); err != nil {
				t.Fatalf("expected FK cascade index %s to exist: %v", name, err)
			}
		}
	})

	t.Run("json_extract_tolerates_empty_string", func(t *testing.T) {
		var isNull bool
		if err := db.QueryRowContext(ctx,
			`SELECT json_extract('', '$.anything') IS NULL`).Scan(&isNull); err != nil {
			t.Fatalf("json_extract('') must not raise: %v", err)
		}
		if !isNull {
			t.Fatal("json_extract('') should return NULL")
		}
		// Valid input is unchanged.
		var val string
		if err := db.QueryRowContext(ctx,
			`SELECT json_extract('{"a":"hi"}', '$.a')`).Scan(&val); err != nil {
			t.Fatalf("json_extract on valid JSON failed: %v", err)
		}
		if val != "hi" {
			t.Fatalf("json_extract valid input = %q, want hi", val)
		}
	})
}
