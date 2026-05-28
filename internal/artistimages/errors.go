package artistimages

import "errors"

var (
	ErrBackfillNotRunning   = errors.New("no artist image backfill is running")
	ErrBackfillNotAvailable = errors.New("artist image backfill is not available")
)
