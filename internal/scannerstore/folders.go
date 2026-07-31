package scannerstore

import (
	"context"
	"fmt"
	"time"
)

// FolderHashes returns each scanned folder's content hash, keyed by the
// folder's path relative to the library root. A folder whose hash is unchanged
// since the last scan can be skipped wholesale.
func (s *Store) FolderHashes(ctx context.Context, libraryID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT folder_path, hash FROM scan_folders WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("load scan folder hashes: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		out[path] = hash
	}
	return out, rows.Err()
}

// FolderPaths lists every folder recorded for the library.
func (s *Store) FolderPaths(ctx context.Context, libraryID string) ([]string, error) {
	return s.stringColumn(ctx, "list scan folders",
		`SELECT folder_path FROM scan_folders WHERE library_id = ?`, libraryID)
}

// SaveFolderHash records a folder's content hash and modification time.
//
// The hash is computed by the caller: it is derived from the folder's entries
// and is scanner logic, not a property of the row.
func (s *Store) SaveFolderHash(ctx context.Context, libraryID, relPath, hash string, modTime time.Time) error {
	_, err := s.exec(ctx, `
		INSERT INTO scan_folders (library_id, folder_path, hash, mod_time, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(library_id, folder_path) DO UPDATE SET
		  hash = excluded.hash,
		  mod_time = excluded.mod_time,
		  updated_at = CURRENT_TIMESTAMP`,
		libraryID, relPath, hash, modTime.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save scan folder %q: %w", relPath, err)
	}
	return nil
}

// DeleteFolderHash forgets a folder, so a later scan treats it as new rather
// than as unchanged-since-last-time.
func (s *Store) DeleteFolderHash(ctx context.Context, libraryID, relPath string) error {
	if _, err := s.exec(ctx,
		`DELETE FROM scan_folders WHERE library_id = ? AND folder_path = ?`,
		libraryID, relPath); err != nil {
		return fmt.Errorf("delete scan folder %q: %w", relPath, err)
	}
	return nil
}
