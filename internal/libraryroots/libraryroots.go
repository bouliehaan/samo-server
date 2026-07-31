// Package libraryroots owns one invariant: a path may only be touched if it
// lives under a configured library root.
//
// It exists as its own package because that invariant had drifted into two
// independent implementations — a hardened one on the read path that resolved
// symlinks and re-checked, and a weaker string-prefix one in the catalog's
// delete path that did not. They agreed only by coincidence (the scanner
// happens to store unresolved paths), and the weaker copy was the one gating
// os.Remove. Two implementations of a security-shaped rule will always drift;
// the fix is for there to be one.
//
// Everything that reads or deletes media goes through Resolver.
package libraryroots

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	// ErrForbidden means the path resolved outside every configured root.
	ErrForbidden = errors.New("path is outside configured libraries")
	// ErrMissing means the path is inside a root but absent from disk.
	ErrMissing = errors.New("file is missing on disk")
	// ErrInvalidPath means the input was empty or resolved to a directory.
	ErrInvalidPath = errors.New("invalid file path")
)

// cacheTTL is how long the resolved root set is reused before it is rebuilt.
//
// Resolving roots costs a SELECT plus an EvalSymlinks and a Stat per library,
// and Validate runs on every media stream, every cover, and every *range*
// request. A client seeking through an audiobook fires dozens of ranges, so
// uncached this put dozens of round-trips (and filesystem calls, possibly
// across a network mount) on the hot path for a set that changes only when the
// operator edits a library.
//
// Staleness is bounded and harmless in both directions: a newly added library
// has no media rows to serve until a scan runs, and a removed one stays
// servable for at most this long. Invalidate() makes CRUD immediate.
const cacheTTL = 5 * time.Second

// Resolver answers "is this path allowed" against the library table, with a
// short-lived cache of the resolved root set. The zero value is not usable;
// construct with New.
type Resolver struct {
	db         *sql.DB
	extraRoots []string

	mu       sync.RWMutex
	roots    []string
	loadedAt time.Time
}

// New returns a Resolver for db. extraRoots are additional always-allowed
// directories outside the library table — the cover cache and podcast cache,
// which Samo owns and serves but which are not libraries.
func New(db *sql.DB, extraRoots ...string) *Resolver {
	return &Resolver{db: db, extraRoots: extraRoots}
}

// Invalidate drops the cached root set so the next call re-reads it. Call
// after any library create/update/delete.
func (r *Resolver) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roots = nil
	r.loadedAt = time.Time{}
}

// Roots returns the resolved allowed-root set, rebuilding it when the cache is
// cold or stale. An error is never cached: a transient database failure must
// not pin an empty root set — which would forbid every path — for the TTL.
func (r *Resolver) Roots(ctx context.Context) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("library root resolver is not configured")
	}

	now := time.Now()
	r.mu.RLock()
	if !r.loadedAt.IsZero() && now.Sub(r.loadedAt) < cacheTTL {
		cached := r.roots
		r.mu.RUnlock()
		return cached, nil
	}
	r.mu.RUnlock()

	roots, err := load(ctx, r.db, r.extraRoots)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.roots = roots
	r.loadedAt = now
	r.mu.Unlock()
	return roots, nil
}

// Validate is the full check every caller should use before opening, serving,
// or deleting a file.
//
// Containment is decided on the *resolved* path. That is the file that will
// actually be opened or removed, so proving it sits inside a root is both
// necessary and sufficient: a symlink planted inside a library pointing at
// /etc/passwd resolves outside every root and is rejected, and a path that was
// never inside a library resolves outside too.
//
// It is deliberately NOT also a hard requirement on the unresolved path.
// Requiring both looks stricter but adds no security — the resolved check
// already catches everything the pair catches — while rejecting legitimate
// media reached by a different-but-equivalent route, e.g. a library configured
// through one symlink and rows recorded through another. That false rejection
// is exactly the failure the old delete-path copy exhibited, silently skipping
// every file it was asked to remove.
//
// Returns the fully resolved path, its FileInfo, and one of the package's
// sentinel errors.
func (r *Resolver) Validate(ctx context.Context, path string) (string, os.FileInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil, ErrInvalidPath
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve path: %w", err)
	}

	roots, err := r.Roots(ctx)
	if err != nil {
		return "", nil, err
	}

	// Whether the path looks contained before resolution. Not authoritative on
	// its own — see the doc comment — but it decides what a *nonexistent* path
	// is told: "missing" for something inside a library, "forbidden" for
	// anything else, so a caller can't probe for the existence of files outside
	// the sandbox by reading the error.
	looksContained := Contains(roots, absolute)

	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		if os.IsNotExist(err) {
			if looksContained {
				return "", nil, ErrMissing
			}
			return "", nil, ErrForbidden
		}
		return "", nil, fmt.Errorf("resolve path: %w", err)
	}
	if !Contains(roots, resolved) {
		return "", nil, ErrForbidden
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, ErrMissing
		}
		return "", nil, fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return "", nil, ErrInvalidPath
	}
	return resolved, info, nil
}

// Contains reports whether path lies at or under any of roots. Both sides are
// cleaned and compared by path segment, so "/mnt/media-other" does not match
// the root "/mnt/media".
func Contains(roots []string, path string) bool {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return false
	}
	for _, root := range roots {
		if within(absolute, root) {
			return true
		}
	}
	return false
}

func within(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// load reads the library table and resolves every root. Rows whose path no
// longer exists on disk are skipped rather than failing the whole set — an
// unmounted NAS should make its own library unreadable, not every other one.
func load(ctx context.Context, db *sql.DB, extraRoots []string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT path FROM libraries WHERE path NOT LIKE 'samo://%'`)
	if err != nil {
		return nil, fmt.Errorf("load library roots: %w", err)
	}
	defer rows.Close()

	var roots []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("scan library root: %w", err)
		}
		if roots, err = appendRoot(roots, path); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, root := range extraRoots {
		if roots, err = appendRoot(roots, root); err != nil {
			return nil, err
		}
	}
	return roots, nil
}

// appendRoot adds a root in BOTH its configured and symlink-resolved forms.
// A library configured as /srv/music that is a symlink to /mnt/tank/music must
// accept media rows recorded under either spelling; recording only one is how
// "delete files" silently skipped everything on such a setup.
func appendRoot(roots []string, root string) ([]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return roots, nil
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		if os.IsNotExist(err) {
			return roots, nil
		}
		return nil, fmt.Errorf("resolve library root %q: %w", root, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return roots, nil
		}
		return nil, fmt.Errorf("stat library root %q: %w", root, err)
	}
	if !info.IsDir() {
		return roots, nil
	}
	roots = appendUnique(roots, filepath.Clean(absolute))
	return appendUnique(roots, filepath.Clean(resolved)), nil
}

func appendUnique(roots []string, root string) []string {
	for _, existing := range roots {
		if existing == root {
			return roots
		}
	}
	return append(roots, root)
}
