package files

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func loadAllowedRoots(ctx context.Context, db *sql.DB, extraRoots []string) ([]string, error) {
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
		var err error
		roots, err = appendAllowedRoot(roots, path)
		if err != nil {
			return nil, err
		}
	}
	for _, root := range extraRoots {
		var err error
		roots, err = appendAllowedRoot(roots, root)
		if err != nil {
			return nil, err
		}
	}
	return roots, rows.Err()
}

func appendAllowedRoot(roots []string, root string) ([]string, error) {
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
	roots = appendUniqueRoot(roots, filepath.Clean(absolute))
	return appendUniqueRoot(roots, filepath.Clean(resolved)), nil
}

func appendUniqueRoot(roots []string, root string) []string {
	for _, existing := range roots {
		if existing == root {
			return roots
		}
	}
	return append(roots, root)
}

func isUnderAllowedRoot(path string, roots []string) bool {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return false
	}
	for _, root := range roots {
		if pathWithinRoot(absolute, root) {
			return true
		}
	}
	return false
}

func pathWithinRoot(path, root string) bool {
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

func validateReadablePath(ctx context.Context, db *sql.DB, extraRoots []string, path string) (string, os.FileInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil, ErrInvalidPath
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve path: %w", err)
	}

	roots, err := loadAllowedRoots(ctx, db, extraRoots)
	if err != nil {
		return "", nil, err
	}
	if !isUnderAllowedRoot(absolute, roots) {
		return "", nil, ErrForbidden
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, ErrMissing
		}
		return "", nil, fmt.Errorf("resolve path: %w", err)
	}
	if !isUnderAllowedRoot(resolved, roots) {
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
