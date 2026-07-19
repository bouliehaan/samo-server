// Package storage owns samo-server's PostgreSQL access: the connection pools,
// the schema migration runner, and the driver-level rewriting that lets the
// rest of the codebase keep writing `?` placeholders (see rewrite.go).
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Pool sizing. The server process holds two pools (Open + OpenReadOnly =
// 2×maxOpenConns), and each scan runs in a subprocess that opens one more write
// pool (maxOpenConns). Even with a couple of concurrent scans the worst case
// stays well under the default max_connections=100.
const (
	maxOpenConns = 16
	maxIdleConns = 8
)

// migrationLockKey is the pg_advisory_lock key that serializes schema
// migration across every process sharing the database: the server, a scan
// subprocess that opens+migrates on launch, and two instances racing during a
// restart. Postgres DDL is not safe to apply from two sessions at once (a bare
// CREATE fails once the other commits, and a killed process could leave a
// half-applied file), so the whole apply sequence runs under this lock. The
// value is arbitrary but must stay stable and distinct from any other advisory
// lock the codebase takes (see storagetest's template lock).
const migrationLockKey = 0x53414D4F4D4947 // ASCII "SAMOMIG"

// Open opens the read-write PostgreSQL pool through the rewriting driver.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("postgres DSN cannot be empty (set SAMO_DB_DSN)")
	}

	db, err := sql.Open(pgDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres database: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	// Recycle connections so a long-lived server survives Postgres restarts and
	// server-side idle-timeout disconnects without surfacing dead conns.
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(time.Hour)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	return db, nil
}

// OpenReadOnly opens a companion pool for read traffic whose sessions are
// pinned read-only (default_transaction_read_only=on). Keeping interactive
// reads on their own pool means a scan or import that saturates the write
// pool's connections can't starve catalog loads — and the server-side
// read-only pin turns any accidental write through this handle into a loud
// error instead of silent state drift.
func OpenReadOnly(ctx context.Context, dsn string) (*sql.DB, error) {
	return Open(ctx, readOnlyDSN(dsn))
}

// readOnlyDSN appends default_transaction_read_only=on to a DSN in either
// URL form (postgres://...?k=v) or keyword form (host=... dbname=...). pgx
// forwards parameters it doesn't recognize to the server as session GUCs.
func readOnlyDSN(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			// Malformed URL: leave it for Open to reject with pgx's error.
			return dsn
		}
		q := u.Query()
		q.Set("default_transaction_read_only", "on")
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dsn + " default_transaction_read_only=on"
}

// ApplyMigrations applies the ordered *.sql migration set to the database,
// recording each file in the schema_migrations ledger so re-runs are no-ops.
//
// The entire sequence runs under a session-level advisory lock (see
// migrationLockKey) so concurrent callers — the server plus a scan subprocess,
// or two instances racing during a restart — serialize instead of colliding on
// a CREATE or half-applying a file. After first run every caller takes the
// lock, sees an empty work list, and releases in well under a millisecond.
func ApplyMigrations(ctx context.Context, db *sql.DB, migrationFS fs.FS) error {
	// A dedicated connection holds the lock for the whole run. Session-level
	// (not xact) because each migration below opens its own transaction.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration lock connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(?)`, migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		// Release on a fresh context so a cancelled ctx still frees the lock
		// before conn.Close() hands the still-locked session back to the pool
		// (Close returns a connection to the pool; it does not end the session).
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock(?)`, migrationLockKey)
	}()

	return applyMigrationsLocked(ctx, db, migrationFS)
}

// applyMigrationsLocked runs the migration sequence. The caller must already
// hold migrationLockKey.
func applyMigrationsLocked(ctx context.Context, db *sql.DB, migrationFS fs.FS) error {
	entries, err := fs.ReadDir(migrationFS, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		// Skip dot/underscore-prefixed files. macOS tar can inject AppleDouble
		// "._foo.sql" sidecars into a build context, and //go:embed *.sql sweeps
		// them in; applying that binary junk as SQL corrupts the connection.
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") ||
			strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	// applied_at is TEXT for portability with rows migrated from the SQLite
	// era; it is only read for human inspection.
	const ledgerDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))`
	if _, err := db.ExecContext(ctx, ledgerDDL); err != nil {
		return fmt.Errorf("ensure schema migrations table: %w", err)
	}

	for _, name := range names {
		applied, err := migrationApplied(ctx, db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		sqlBytes, err := fs.ReadFile(migrationFS, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := applyMigration(ctx, db, name, string(sqlBytes)); err != nil {
			return err
		}
	}

	return nil
}

func migrationApplied(ctx context.Context, db *sql.DB, version string) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = ?`, version).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("check migration %s: %w", version, err)
}

func applyMigration(ctx context.Context, db *sql.DB, version string, body string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", version, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("apply migration %s: %w", version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}

	return nil
}
