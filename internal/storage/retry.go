package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsRetryable reports whether err is transient contention Postgres asks the
// client to retry: a serialization failure (40001) or a deadlock in which this
// transaction was chosen as the victim (40P01). Concurrent writers — a scan,
// the explo pipeline, and interactive requests touching the same rows — can
// produce these under load; the statement is safe to re-run.
func IsRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40001" || pgErr.Code == "40P01"
	}
	return false
}

// Retry runs fn until it succeeds, ctx is done, or attempts is exhausted.
// Retryable errors (see IsRetryable) back off linearly; anything else returns
// immediately.
func Retry(ctx context.Context, attempts int, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var last error
	for i := 0; i < attempts; i++ {
		if err := fn(); err != nil {
			last = err
			if !IsRetryable(err) || i == attempts-1 {
				return err
			}
			wait := time.Duration(i+1) * 250 * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		return nil
	}
	return last
}

// WithRetryTx runs fn inside a transaction — committing on success, rolling
// back on any error — and retries the whole transaction when Postgres reports
// transient contention (see IsRetryable). A serialization failure or deadlock
// aborts the entire transaction, so a retry re-runs every statement in a fresh
// tx; fn must therefore be safe to run more than once. Errors fn returns that
// are not retryable (including domain sentinels like a not-found error)
// propagate immediately without a retry.
//
// This is the transaction-shaped companion to Retry: reach for it wherever a
// multi-statement write must be atomic AND may race the scanner or explo
// pipeline on the same rows.
func WithRetryTx(ctx context.Context, db *sql.DB, attempts int, fn func(*sql.Tx) error) error {
	return Retry(ctx, attempts, func() error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	})
}
