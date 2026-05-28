package scanner

import (
	"context"
	"os"
	"time"
)

// Keep this short: missing/prune phases may call this for thousands of rows.
// Long timeouts make scans appear frozen for hours on stale network paths.
const pathStatTimeout = 350 * time.Millisecond

// statWithTimeout returns file metadata or an error. Network mounts may block
// os.Stat indefinitely; callers should treat errors as "unavailable".
func statWithTimeout(path string, timeout time.Duration) (os.FileInfo, error) {
	if timeout <= 0 {
		timeout = pathStatTimeout
	}
	type result struct {
		info os.FileInfo
		err  error
	}
	done := make(chan result, 1)
	go func() {
		info, err := os.Stat(path)
		done <- result{info: info, err: err}
	}()
	select {
	case res := <-done:
		return res.info, res.err
	case <-time.After(timeout):
		return nil, os.ErrDeadlineExceeded
	}
}

// fileReachable reports whether path can be stat'd. Network mounts may block
// forever; treat timeouts as reachable so scans keep moving.
func fileReachable(ctx context.Context, path string) bool {
	if err := ctx.Err(); err != nil {
		return true
	}
	_, err := statWithTimeout(path, pathStatTimeout)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	// Timeout or I/O error: assume reachable so we do not delete/mark on a slow mount.
	return true
}
