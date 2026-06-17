package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	// modernc.org/sqlite is a pure-Go SQLite driver — no CGO, no libc
	// dependency. This is what lets samo-server ship as a single statically-
	// linked binary on any Linux distro.
	_ "modernc.org/sqlite"
)

type OpenOptions struct {
	ReadOnly     bool
	MaxOpenConns int
	MaxIdleConns int
	BusyTimeout  time.Duration
	ForeignKeys  bool
	JournalMode  string
	Synchronous  string
	CacheShared  bool
}

var defaultOpenOptions = OpenOptions{
	MaxOpenConns: 16,
	MaxIdleConns: 8,
	BusyTimeout:  60 * time.Second,
	ForeignKeys:  true,
	JournalMode:  "WAL",
	Synchronous:  "NORMAL",
	CacheShared:  false,
}

func Open(ctx context.Context, path string) (*sql.DB, error) {
	return OpenWithOptions(ctx, path, defaultOpenOptions)
}

func OpenReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	opts := defaultOpenOptions
	opts.ReadOnly = true
	return OpenWithOptions(ctx, path, opts)
}

func OpenWithOptions(ctx context.Context, path string, opts OpenOptions) (*sql.DB, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("database path cannot be empty")
	}
	if !opts.ReadOnly {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	// _pragma query params apply to every connection in the pool. Exec'ing PRAGMA
	// once after Open only affects the first pooled connection, which caused
	// SQLITE_BUSY under scan load when progress and catalog writes used other conns.
	db, err := sql.Open("sqlite", sqliteDSN(path, opts))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	if opts.MaxOpenConns > 0 {
		db.SetMaxOpenConns(opts.MaxOpenConns)
	}
	if opts.MaxIdleConns > 0 {
		db.SetMaxIdleConns(opts.MaxIdleConns)
	}

	// Ensure WAL on existing databases created before DSN pragmas.
	if !opts.ReadOnly {
		if _, err := db.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply sqlite journal_mode: %w", err)
		}
	}

	return db, nil
}

func sqliteDSN(path string, opts OpenOptions) string {
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	u := url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(abs),
	}
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", int(opts.BusyTimeout.Milliseconds())))
	q.Add("_pragma", fmt.Sprintf("foreign_keys(%s)", boolToOnOff(opts.ForeignKeys)))
	if opts.ReadOnly {
		q.Add("mode", "ro")
		q.Add("_pragma", "query_only(ON)")
	} else {
		q.Add("_pragma", fmt.Sprintf("journal_mode(%s)", opts.JournalMode))
		q.Add("_pragma", fmt.Sprintf("synchronous(%s)", opts.Synchronous))
		q.Add("_txlock", "immediate")
	}
	if opts.CacheShared {
		q.Add("cache", "shared")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func boolToOnOff(value bool) string {
	if value {
		return "ON"
	}
	return "OFF"
}

func ApplyMigrations(ctx context.Context, db *sql.DB, migrationFS fs.FS) error {
	entries, err := fs.ReadDir(migrationFS, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
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
