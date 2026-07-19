// Package storagetest hands each test its own throwaway PostgreSQL database.
//
// The suite runs against the same engine, driver, and schema as production —
// that is the whole point of retiring SQLite from the tests. To keep ~100
// database-backed tests fast, the schema is migrated once into a template
// database (named after a hash of the migration set, so it rebuilds itself
// whenever migrations change) and every test clones it with CREATE DATABASE
// ... TEMPLATE, which is a file-level copy measured in tens of milliseconds.
//
// Point SAMO_TEST_PG_DSN at any Postgres superuser/owner account, or run the
// default disposable container:
//
//	make test-db
package storagetest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

// EnvDSN names the environment variable that overrides the default test DSN.
const EnvDSN = "SAMO_TEST_PG_DSN"

// defaultDSN matches the disposable container `make test-db` starts. Port
// 55432 deliberately avoids colliding with a real Postgres on 5432.
const defaultDSN = "postgres://samo:samo@localhost:55432/samo?sslmode=disable"

// advisoryLockKey serializes template creation across concurrently running
// `go test` package processes. Arbitrary but stable.
const advisoryLockKey = 762_617_109

var (
	initOnce sync.Once
	admin    *sql.DB // maintenance pool used for CREATE/DROP DATABASE
	adminDSN string
	tplName  string
	initErr  error

	seq atomic.Int64
)

// Open returns a *sql.DB (through the same rewriting pgx driver production
// uses) connected to a freshly cloned, fully migrated database that no other
// test shares. The database is dropped when the test finishes.
func Open(t testing.TB) *sql.DB {
	t.Helper()
	db, _ := OpenWithDSN(t)
	return db
}

// OpenWithDSN is Open for tests that also need the database's DSN — e.g. to
// open a second handle the way production opens its read-only pool.
func OpenWithDSN(t testing.TB) (*sql.DB, string) {
	t.Helper()
	initOnce.Do(setup)
	if initErr != nil {
		t.Fatalf("storagetest: %v", initErr)
	}
	ctx := context.Background()

	name := fmt.Sprintf("samo_test_%d_%d", os.Getpid(), seq.Add(1))
	if err := retryBusy(ctx, func() error {
		_, err := admin.ExecContext(ctx, fmt.Sprintf(`CREATE DATABASE %s TEMPLATE %s`, name, tplName))
		return err
	}); err != nil {
		t.Fatalf("storagetest: clone %s from template: %v", name, err)
	}

	dsn := dsnForDatabase(adminDSN, name)
	db, err := storage.Open(ctx, dsn)
	if err != nil {
		_, _ = admin.ExecContext(ctx, `DROP DATABASE IF EXISTS `+name)
		t.Fatalf("storagetest: open %s: %v", name, err)
	}
	// A single test never needs production's pool width, and go test runs many
	// packages (each with many tests) against one Postgres; stay well under
	// the server's max_connections.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(2)

	t.Cleanup(func() {
		db.Close()
		// FORCE kicks any connection a leaky goroutine still holds (PG 13+).
		if _, err := admin.ExecContext(context.Background(),
			fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name)); err != nil {
			t.Logf("storagetest: drop %s: %v", name, err)
		}
	})
	return db, dsn
}

// setup connects the maintenance pool and ensures the template database for
// the current migration set exists, building it if this process is first.
func setup() {
	adminDSN = strings.TrimSpace(os.Getenv(EnvDSN))
	if adminDSN == "" {
		adminDSN = defaultDSN
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var err error
	admin, err = storage.Open(ctx, adminDSN)
	if err != nil {
		initErr = fmt.Errorf(
			"cannot reach the test Postgres at %s: %v\n\n"+
				"Tests run against a real PostgreSQL. Start the disposable container with:\n"+
				"    make test-db\n"+
				"(equivalent: docker run -d --name samo-test-pg -e POSTGRES_USER=samo -e POSTGRES_PASSWORD=samo -e POSTGRES_DB=samo -p 55432:5432 postgres:16)\n"+
				"or set "+EnvDSN+" to point at your own server.",
			redact(adminDSN), err)
		return
	}
	// The maintenance pool only ever runs short CREATE/DROP statements; don't
	// let it hoard connections other package processes need.
	admin.SetMaxOpenConns(2)
	admin.SetMaxIdleConns(1)

	hash, err := migrationsHash()
	if err != nil {
		initErr = fmt.Errorf("hash migrations: %w", err)
		return
	}
	tplName = "samo_tpl_" + hash

	if initErr = ensureTemplate(ctx); initErr != nil {
		return
	}
}

// ensureTemplate builds the migrated template database if it doesn't exist
// yet, holding a cross-process advisory lock so exactly one `go test` process
// does the work. Creation goes through a temporary name and a final rename:
// a process killed mid-migration leaves only a junk samo_tpl_*_tmp_* database
// (swept below), never a half-migrated template under the final name.
func ensureTemplate(ctx context.Context) error {
	lock, err := admin.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire lock conn: %w", err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(ctx, `SELECT pg_advisory_lock(?)`, advisoryLockKey); err != nil {
		return fmt.Errorf("pg_advisory_lock: %w", err)
	}
	defer lock.ExecContext(ctx, `SELECT pg_advisory_unlock(?)`, advisoryLockKey)

	sweepStale(ctx)

	var one int
	err = admin.QueryRowContext(ctx, `SELECT 1 FROM pg_database WHERE datname = ?`, tplName).Scan(&one)
	if err == nil {
		return nil // template already built by an earlier run/process
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("check template: %w", err)
	}

	tmp := fmt.Sprintf("%s_tmp_%d", tplName, os.Getpid())
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+tmp); err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	tplDB, err := storage.Open(ctx, dsnForDatabase(adminDSN, tmp))
	if err != nil {
		return fmt.Errorf("connect %s: %w", tmp, err)
	}
	if err := storage.ApplyMigrations(ctx, tplDB, migrations.Files); err != nil {
		tplDB.Close()
		return fmt.Errorf("migrate template: %w", err)
	}
	tplDB.Close()

	// The rename fails while the server is still reaping the migration pool's
	// backends, so give it the same brief patience as cloning.
	if err := retryBusy(ctx, func() error {
		_, err := admin.ExecContext(ctx, fmt.Sprintf(`ALTER DATABASE %s RENAME TO %s`, tmp, tplName))
		return err
	}); err != nil {
		return fmt.Errorf("finalize template: %w", err)
	}
	return nil
}

// sweepStale drops leftovers from previous runs: templates for migration sets
// that no longer exist (schema changed), abandoned _tmp_ builds, and per-test
// databases whose creating process is gone (a killed `go test`). Runs under
// the advisory lock; drops are best-effort and never FORCE, so anything a
// live process is actually using survives.
func sweepStale(ctx context.Context) {
	rows, err := admin.QueryContext(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'samo_tpl_%' OR datname LIKE 'samo_test_%'`)
	if err != nil {
		return
	}
	var victims []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			continue
		}
		switch {
		case name == tplName:
			// current template — keep
		case strings.HasPrefix(name, "samo_test_"):
			if pid, ok := testDBPid(name); ok && !processAlive(pid) {
				victims = append(victims, name)
			}
		default:
			victims = append(victims, name) // old-hash template or orphaned tmp build
		}
	}
	rows.Close()
	for _, name := range victims {
		_, _ = admin.ExecContext(ctx, `DROP DATABASE IF EXISTS `+name)
	}
}

var testDBPattern = regexp.MustCompile(`^samo_test_(\d+)_\d+$`)

func testDBPid(name string) (int, bool) {
	m := testDBPattern.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	pid, err := strconv.Atoi(m[1])
	return pid, err == nil
}

// processAlive reports whether pid exists on THIS machine (signal 0). Tests
// normally run against a local disposable container, so a dead pid means a
// crashed test run whose databases are safe to reap. When in doubt (e.g. the
// error isn't ESRCH), the database is kept.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err != syscall.ESRCH
}

// retryBusy retries CREATE/ALTER DATABASE briefly while the server finishes
// reaping just-closed backends of the source/target database.
func retryBusy(ctx context.Context, fn func() error) error {
	var err error
	for i := 0; i < 10; i++ {
		if err = fn(); err == nil {
			return nil
		}
		msg := err.Error()
		if !strings.Contains(msg, "is being accessed by other users") &&
			!strings.Contains(msg, "source database") {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(200 * time.Millisecond):
		}
	}
	return err
}

// migrationsHash fingerprints the embedded migration set (names + bytes) so
// the template database is rebuilt exactly when the schema lineage changes.
func migrationsHash() (string, error) {
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		b, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// dsnForDatabase returns dsn pointed at a different database name, handling
// both URL (postgres://…/db) and keyword (host=… dbname=db) forms.
func dsnForDatabase(dsn, name string) string {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err == nil {
			u.Path = "/" + name
			return u.String()
		}
	}
	re := regexp.MustCompile(`\bdbname=\S+`)
	if re.MatchString(dsn) {
		return re.ReplaceAllString(dsn, "dbname="+name)
	}
	return dsn + " dbname=" + name
}

func redact(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "****")
			return u.String()
		}
	}
	return dsn
}
