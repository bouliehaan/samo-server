package storage

import (
	"context"
	"strings"
	"time"
)

// IsBusy reports whether err is SQLite lock contention (SQLITE_BUSY / "database is locked").
func IsBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "database schema is locked") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked") ||
		strings.Contains(msg, "code 5") ||
		strings.Contains(msg, "code 6")
}

// Retry runs fn until it succeeds, ctx is done, or attempts is exhausted. Busy errors
// are retried with linear backoff so scan progress and terminal job rows can persist
// under heavy write load.
func Retry(ctx context.Context, attempts int, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var last error
	for i := 0; i < attempts; i++ {
		if err := fn(); err != nil {
			last = err
			if !IsBusy(err) || i == attempts-1 {
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
