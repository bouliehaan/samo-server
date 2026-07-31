// Package scannerstore is the scanner's persistence layer.
//
// The scanner decides what an entity is, applies override policy, and accounts
// for what it saw. This package runs the statement and reports what the
// database did. Nothing here holds scan state, so a store method is safe to
// call from any phase, in any order, concurrently.
//
// Methods take primitives or catalog types — never scanner types. That is not
// only to avoid an import cycle: a type that carries behaviour (albumFolder
// with its hash(), catalog.OverrideIndex with its lookups) belongs on the
// deciding side, and passing it down here would drag policy into persistence.
//
// Reads live in catalogstore; this is the write path a scan takes.
package scannerstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage"
)

// Store holds the scan write path's statements.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB exposes the underlying pool for the call sites that still need it —
// override guards, and the handful of scanner queries not yet moved here.
// Every use is a place this package has not finished absorbing.
func (s *Store) DB() *sql.DB {
	return s.db
}

// exec runs a write with transient-contention retry. Catalog upserts share the
// pool with progress writes, the parallel music phase, and background
// enrichment; under that contention a single serialization failure used to
// abort a whole scan mid-artist-upsert. Retrying is the same policy the
// progress writer uses (see storage.IsRetryable).
func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	var result sql.Result
	err := storage.Retry(ctx, 8, func() error {
		var execErr error
		result, execErr = s.db.ExecContext(ctx, query, args...)
		return execErr
	})
	return result, err
}

// jsonText encodes a value for a *_json column. A marshal failure stores an
// empty object rather than failing the write: these columns are enrichment
// (genres, images, external ids), and losing one must not cost the row.
func jsonText(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// boolInt renders a bool for the integer-typed flag columns the schema uses.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// nullableString maps "" to NULL, so an absent foreign key is a real NULL
// rather than an empty string that satisfies no join.
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// timeString renders an optional timestamp as UTC RFC3339, or NULL.
func timeString(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}
