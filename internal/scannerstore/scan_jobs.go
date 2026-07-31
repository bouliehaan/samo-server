package scannerstore

import (
	"context"
	"database/sql"

	"github.com/bouliehaan/samo-server/internal/storage"
)

// SetScanJobFilesSeen records a running scan's file count so the dashboard can
// show live progress.
//
// The status filter is what makes this safe to call from a subprocess that may
// outlive the job: once a job is finished or cancelled, its counter freezes at
// the value it had rather than being moved by a straggler write.
//
// This takes a *sql.DB rather than using the Store's, because the scan
// subprocess owns its own short-lived connection to the same database.
func SetScanJobFilesSeen(ctx context.Context, db *sql.DB, jobID string, total int) error {
	return storage.Retry(ctx, 10, func() error {
		_, err := db.ExecContext(ctx,
			`UPDATE scan_jobs SET files_seen = ? WHERE id = ? AND status IN ('running', 'pending')`,
			total, jobID)
		return err
	})
}
