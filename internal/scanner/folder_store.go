package scanner

import (
	"context"
	"fmt"
	"strings"
)

func (s *Scanner) loadFolderHashes(ctx context.Context, libraryID string) (map[string]string, error) {
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

func (s *Scanner) saveFolderHash(ctx context.Context, libraryID string, folder albumFolder) error {
	hash := folder.hash()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO scan_folders (library_id, folder_path, hash, mod_time, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(library_id, folder_path) DO UPDATE SET
		  hash = excluded.hash,
		  mod_time = excluded.mod_time,
		  updated_at = CURRENT_TIMESTAMP`,
		libraryID, folder.relPath, hash, folder.modTime.UTC().Format("2006-01-02T15:04:05Z07:00"))
	if err != nil {
		return fmt.Errorf("save scan folder %q: %w", folder.relPath, err)
	}
	return nil
}

func (s *Scanner) markMissingFolders(ctx context.Context, libraryID string, seenFolders map[string]struct{}) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT folder_path FROM scan_folders WHERE library_id = ?`, libraryID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return err
		}
		if _, ok := seenFolders[path]; !ok {
			// Folder removed from disk; drop hash so a future rescan can clean up.
			_, _ = s.db.ExecContext(ctx,
				`DELETE FROM scan_folders WHERE library_id = ? AND folder_path = ?`,
				libraryID, path)
		}
	}
	return rows.Err()
}

func folderSeenKey(relPath string) string {
	return strings.TrimSpace(relPath)
}
