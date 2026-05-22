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
		absolute, err := filepath.Abs(strings.TrimSpace(path))
		if err != nil {
			return nil, err
		}
		roots = append(roots, absolute)
	}
	for _, root := range extraRoots {
		absolute, err := filepath.Abs(strings.TrimSpace(root))
		if err != nil {
			return nil, err
		}
		if absolute != "" {
			roots = append(roots, absolute)
		}
	}
	return roots, rows.Err()
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
	return !strings.HasPrefix(rel, "..")
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

	info, err := os.Stat(absolute)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, ErrMissing
		}
		return "", nil, fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return "", nil, ErrInvalidPath
	}
	return absolute, info, nil
}
