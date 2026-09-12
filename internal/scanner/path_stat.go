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

// pathState is what a single stat could prove about a path. Prune deletes
// rows, so the difference between "proven gone" and "could not tell" decides
// whether a library survives an unplugged drive.
type pathState int

const (
	// pathPresent: the stat succeeded, the file is still on disk.
	pathPresent pathState = iota
	// pathGone: the stat said the path does not exist. On a reachable volume
	// that is proof the file was deleted.
	pathGone
	// pathUnknown: the stat timed out or failed some other way — a slow NFS
	// mount, an I/O error, a permission change. Nothing can be concluded, so
	// callers must not delete anything.
	pathUnknown
)

// classifyPath stats path and reports what that proved.
func classifyPath(ctx context.Context, path string) pathState {
	// A cancelled scan gets no say in what is gone.
	if err := ctx.Err(); err != nil {
		return pathUnknown
	}
	_, err := statWithTimeout(path, pathStatTimeout)
	switch {
	case err == nil:
		return pathPresent
	case os.IsNotExist(err):
		return pathGone
	default:
		return pathUnknown
	}
}
