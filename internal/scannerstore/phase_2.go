package scannerstore

import (
	"context"
	"fmt"
	"strings"
)

// IndexedMediaFile is the identity of an indexed file as phase 2 sees it —
// enough to decide whether two rows are the same recording that moved.
type IndexedMediaFile struct {
	ID          string
	LibraryID   string
	Path        string
	TrackID     string
	TrackPID    string
	ContentHash string
	Missing     bool
}

// MovedFileFields are the on-disk facts a moved file carries to its new home.
type MovedFileFields struct {
	RelativePath     string
	Checksum         string
	ContentHash      string
	EmbeddedTagsJSON string
	SizeBytes        int64
	ModifiedAt       *string
}

// PresentTrackedPaths lists the library's music files that are not marked
// missing — the paths a walk is expected to have visited again.
func (s *Store) PresentTrackedPaths(ctx context.Context, libraryID string) ([]string, error) {
	return s.stringColumn(ctx, "list media files for missing mark",
		`SELECT path FROM media_files WHERE library_id = ? AND missing = 0 AND track_id IS NOT NULL`,
		libraryID)
}

// MarkMediaFilesMissing flags a batch of paths missing and reports how many
// rows changed. Paths already marked are excluded, so the count is the number
// newly gone rather than the number named.
func (s *Store) MarkMediaFilesMissing(ctx context.Context, libraryID string, paths []string) (int, error) {
	if len(paths) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(paths)+1)
	args = append(args, libraryID)
	for _, path := range paths {
		args = append(args, path)
	}
	query := fmt.Sprintf(`
		UPDATE media_files
		SET missing = 1,
		    missing_detected_at = COALESCE(missing_detected_at, CURRENT_TIMESTAMP),
		    updated_at = CURRENT_TIMESTAMP
		WHERE library_id = ? AND missing = 0 AND path IN (%s)`,
		strings.TrimSuffix(strings.Repeat("?,", len(paths)), ","))
	res, err := s.exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("batch mark missing: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// TrackedFilesByPID returns the library's music files that carry a persistent
// track id, grouped-ready: ordered by pid, missing rows first within each pid,
// then by path. The caller walks the ordering to pair each missing row with a
// newly indexed one.
func (s *Store) TrackedFilesByPID(ctx context.Context, libraryID string) ([]IndexedMediaFile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, path, track_id, track_pid, content_hash, missing
		FROM media_files
		WHERE library_id = ? AND track_id IS NOT NULL AND TRIM(track_pid) != ''
		ORDER BY track_pid, missing DESC, path`,
		libraryID)
	if err != nil {
		return nil, fmt.Errorf("load media files for missing-track phase: %w", err)
	}
	defer rows.Close()

	var out []IndexedMediaFile
	for rows.Next() {
		var row IndexedMediaFile
		var missing int
		if err := rows.Scan(&row.ID, &row.Path, &row.TrackID, &row.TrackPID, &row.ContentHash, &missing); err != nil {
			return nil, err
		}
		row.LibraryID = libraryID
		row.Missing = missing != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

// MissingTrackedFiles lists files still marked missing across every library,
// the input to the cross-library move search.
func (s *Store) MissingTrackedFiles(ctx context.Context) ([]IndexedMediaFile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, library_id, path, track_id, track_pid, content_hash
		FROM media_files
		WHERE missing = 1 AND track_id IS NOT NULL AND TRIM(track_pid) != ''`)
	if err != nil {
		return nil, fmt.Errorf("list cross-library missing tracks: %w", err)
	}
	defer rows.Close()

	var out []IndexedMediaFile
	for rows.Next() {
		var row IndexedMediaFile
		if err := rows.Scan(&row.ID, &row.LibraryID, &row.Path, &row.TrackID, &row.TrackPID, &row.ContentHash); err != nil {
			return nil, err
		}
		row.Missing = true
		out = append(out, row)
	}
	return out, rows.Err()
}

// EmbeddedTagsJSON returns a file's raw embedded-tag blob.
func (s *Store) EmbeddedTagsJSON(ctx context.Context, fileID string) (string, error) {
	var tagsJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT embedded_tags_json FROM media_files WHERE id = ?`, fileID).Scan(&tagsJSON)
	return tagsJSON, err
}

// RecentByMusicBrainzID finds candidates in *other* libraries carrying the same
// MusicBrainz track or recording id.
//
// json_extract is not a Postgres builtin — it is a SQLite-compat shim the
// schema defines (migration 0006), which is also where its empty-string
// handling lives.
func (s *Store) RecentByMusicBrainzID(ctx context.Context, excludeLibraryID, mbzID string) ([]IndexedMediaFile, error) {
	return s.candidates(ctx, `
		SELECT id, library_id, path, track_id, track_pid, content_hash
		FROM media_files
		WHERE missing = 0 AND library_id != ? AND (
			json_extract(embedded_tags_json, '$.musicbrainz_trackid') = ? OR
			json_extract(embedded_tags_json, '$.musicbrainz_recordingid') = ?
		)
		ORDER BY updated_at DESC LIMIT 8`,
		excludeLibraryID, mbzID, mbzID)
}

// RecentByContentHash finds candidates in other libraries with the same
// metadata fingerprint.
func (s *Store) RecentByContentHash(ctx context.Context, excludeLibraryID, contentHash string) ([]IndexedMediaFile, error) {
	return s.candidates(ctx, `
		SELECT id, library_id, path, track_id, track_pid, content_hash
		FROM media_files
		WHERE missing = 0 AND library_id != ? AND content_hash = ?
		ORDER BY updated_at DESC LIMIT 8`,
		excludeLibraryID, contentHash)
}

// RecentByFileName finds candidates in other libraries with the same base name.
func (s *Store) RecentByFileName(ctx context.Context, excludeLibraryID, fileName string) ([]IndexedMediaFile, error) {
	return s.candidates(ctx, `
		SELECT id, library_id, path, track_id, track_pid, content_hash
		FROM media_files
		WHERE missing = 0 AND library_id != ? AND file_name = ?
		ORDER BY updated_at DESC LIMIT 8`,
		excludeLibraryID, fileName)
}

func (s *Store) candidates(ctx context.Context, query string, args ...any) ([]IndexedMediaFile, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []IndexedMediaFile
	for rows.Next() {
		var row IndexedMediaFile
		if err := rows.Scan(&row.ID, &row.LibraryID, &row.Path, &row.TrackID, &row.TrackPID, &row.ContentHash); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// MovedFileFieldsFor reads the on-disk facts recorded for a file.
func (s *Store) MovedFileFieldsFor(ctx context.Context, fileID string) (MovedFileFields, error) {
	var f MovedFileFields
	err := s.db.QueryRowContext(ctx, `
		SELECT relative_path, checksum, content_hash, embedded_tags_json, size_bytes, modified_at
		FROM media_files WHERE id = ?`, fileID).Scan(
		&f.RelativePath, &f.Checksum, &f.ContentHash, &f.EmbeddedTagsJSON, &f.SizeBytes, &f.ModifiedAt)
	if err != nil {
		return f, fmt.Errorf("load matched file %q: %w", fileID, err)
	}
	return f, nil
}

// DeleteMediaFileInLibrary removes one file row, scoped to its library.
func (s *Store) DeleteMediaFileInLibrary(ctx context.Context, fileID, libraryID string) error {
	if _, err := s.exec(ctx,
		`DELETE FROM media_files WHERE id = ? AND library_id = ?`, fileID, libraryID); err != nil {
		return fmt.Errorf("delete duplicate media file %q: %w", fileID, err)
	}
	return nil
}

// MoveMediaFile relocates a file row to a new path, clearing its missing mark.
// The row keeps its id and track_id — that is the point of the move: the file
// travelled, its identity did not.
//
// The error is returned unwrapped; only the caller knows which path the file
// moved *from*, which is half of a useful message.
func (s *Store) MoveMediaFile(ctx context.Context, fileID, libraryID, path, fileName, inode string, fields MovedFileFields) error {
	_, err := s.exec(ctx, `
		UPDATE media_files
		SET library_id = ?,
		    path = ?,
		    relative_path = ?,
		    file_name = ?,
		    inode = ?,
		    size_bytes = ?,
		    modified_at = ?,
		    checksum = ?,
		    content_hash = ?,
		    embedded_tags_json = ?,
		    missing = 0,
		    missing_detected_at = NULL,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		libraryID, path, fields.RelativePath, fileName, inode,
		fields.SizeBytes, fields.ModifiedAt, fields.Checksum, fields.ContentHash, fields.EmbeddedTagsJSON,
		fileID)
	return err
}

// DeleteMusicTrackIgnoringError drops a track row that a move superseded.
//
// The error is deliberately dropped: the move itself already succeeded, and the
// leftover track is picked up by the orphan prune at the end of the scan.
func (s *Store) DeleteMusicTrackIgnoringError(ctx context.Context, trackID string) {
	_, _ = s.exec(ctx, `DELETE FROM music_tracks WHERE id = ?`, trackID)
}
