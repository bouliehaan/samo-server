package libraries

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type MissingFile struct {
	ID                string     `json:"id"`
	LibraryID         string     `json:"libraryId"`
	Path              string     `json:"path"`
	RelativePath      string     `json:"relativePath,omitempty"`
	TrackID           string     `json:"trackId,omitempty"`
	TrackTitle        string     `json:"trackTitle,omitempty"`
	AlbumTitle        string     `json:"albumTitle,omitempty"`
	MissingDetectedAt *time.Time `json:"missingDetectedAt,omitempty"`
}

type MissingFilePage struct {
	Items  []MissingFile `json:"items"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

func (s *Service) ListMissingFiles(ctx context.Context, libraryID string, limit, offset int) (MissingFilePage, error) {
	libraryID = strings.TrimSpace(libraryID)
	limit, offset = normalizePage(limit, offset)

	countQuery := `SELECT COUNT(*) FROM media_files WHERE missing = 1`
	listQuery := `
		SELECT mf.id, mf.library_id, mf.path, mf.relative_path, mf.track_id, mf.missing_detected_at,
		       COALESCE(mt.title, ''), COALESCE(ma.title, '')
		FROM media_files mf
		LEFT JOIN music_tracks mt ON mt.id = mf.track_id
		LEFT JOIN music_albums ma ON ma.id = mt.album_id
		WHERE mf.missing = 1`
	args := make([]any, 0, 4)
	if libraryID != "" {
		countQuery += ` AND library_id = ?`
		listQuery += ` AND mf.library_id = ?`
		args = append(args, libraryID)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return MissingFilePage{}, fmt.Errorf("count missing files: %w", err)
	}

	listQuery += ` ORDER BY mf.missing_detected_at DESC, mf.path COLLATE NOCASE LIMIT ? OFFSET ?`
	listArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return MissingFilePage{}, fmt.Errorf("list missing files: %w", err)
	}
	defer rows.Close()

	items := make([]MissingFile, 0)
	for rows.Next() {
		var item MissingFile
		var trackID sql.NullString
		var detected sql.NullString
		if err := rows.Scan(
			&item.ID,
			&item.LibraryID,
			&item.Path,
			&item.RelativePath,
			&trackID,
			&detected,
			&item.TrackTitle,
			&item.AlbumTitle,
		); err != nil {
			return MissingFilePage{}, fmt.Errorf("scan missing file: %w", err)
		}
		if trackID.Valid {
			item.TrackID = trackID.String
		}
		if detected.Valid {
			if parsed, err := time.Parse(time.RFC3339, detected.String); err == nil {
				item.MissingDetectedAt = &parsed
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return MissingFilePage{}, err
	}

	return MissingFilePage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

type RemoveMissingFilesResult struct {
	Removed int `json:"removed"`
}

// RemoveAllMissingFiles deletes every media_files row flagged missing, optionally
// scoped to one library. Orphan music rows are pruned once at the end.
func (s *Service) RemoveAllMissingFiles(ctx context.Context, libraryID string) (RemoveMissingFilesResult, error) {
	libraryID = strings.TrimSpace(libraryID)
	query := `DELETE FROM media_files WHERE missing = 1`
	args := []any{}
	if libraryID != "" {
		query += ` AND library_id = ?`
		args = append(args, libraryID)
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return RemoveMissingFilesResult{}, fmt.Errorf("delete missing media files: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return RemoveMissingFilesResult{}, fmt.Errorf("missing file delete rows: %w", err)
	}
	if rows > 0 {
		if _, err := s.scanner.PruneOrphanMusic(ctx); err != nil {
			return RemoveMissingFilesResult{Removed: int(rows)}, fmt.Errorf("prune orphan music after missing file removal: %w", err)
		}
	}
	return RemoveMissingFilesResult{Removed: int(rows)}, nil
}

func (s *Service) RemoveMissingFile(ctx context.Context, fileID string) error {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return ErrNotFound
	}

	var missing int
	err := s.db.QueryRowContext(ctx, `SELECT missing FROM media_files WHERE id = ?`, fileID).Scan(&missing)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load media file: %w", err)
	}
	if missing == 0 {
		return fmt.Errorf("%w: file is not marked missing", ErrInvalidLibrary)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM media_files WHERE id = ?`, fileID); err != nil {
		return fmt.Errorf("delete missing media file: %w", err)
	}
	if _, err := s.scanner.PruneOrphanMusic(ctx); err != nil {
		return fmt.Errorf("prune orphan music after missing file removal: %w", err)
	}
	return nil
}
