package scannerstore

import (
	"context"
	"fmt"
)

// CountMediaFilesForLibrary reports how many files the library has indexed.
// The scanner uses it as a sanity check before pruning: a walk that found
// nothing against an index that holds thousands means an unmounted volume, not
// a deleted library.
func (s *Store) CountMediaFilesForLibrary(ctx context.Context, libraryID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_files WHERE library_id = ?`, libraryID).Scan(&count)
	return count, err
}

// PresentMediaFilePaths lists the library's paths that are not already marked
// missing — the set a scan is expected to have seen again.
func (s *Store) PresentMediaFilePaths(ctx context.Context, libraryID string) ([]string, error) {
	return s.stringColumn(ctx, "list media files for prune",
		`SELECT path FROM media_files WHERE library_id = ? AND missing = 0`, libraryID)
}

// AudiobookIDsForLibrary lists every audiobook in the library.
func (s *Store) AudiobookIDsForLibrary(ctx context.Context, libraryID string) ([]string, error) {
	return s.stringColumn(ctx, "list audiobooks for prune",
		`SELECT id FROM audiobooks WHERE library_id = ?`, libraryID)
}

// PodcastIDsForLibrary lists every podcast show in the library.
func (s *Store) PodcastIDsForLibrary(ctx context.Context, libraryID string) ([]string, error) {
	return s.stringColumn(ctx, "list podcasts for prune",
		`SELECT id FROM podcasts WHERE library_id = ?`, libraryID)
}

// PodcastEpisodeIDsForLibrary lists every episode in the library.
func (s *Store) PodcastEpisodeIDsForLibrary(ctx context.Context, libraryID string) ([]string, error) {
	return s.stringColumn(ctx, "list podcast episodes for prune",
		`SELECT id FROM podcast_episodes WHERE library_id = ?`, libraryID)
}

// DeleteMediaFileByPath removes a file row the scan proved is gone.
func (s *Store) DeleteMediaFileByPath(ctx context.Context, libraryID, path string) error {
	if _, err := s.exec(ctx, `DELETE FROM media_files WHERE library_id = ? AND path = ?`, libraryID, path); err != nil {
		return fmt.Errorf("delete stale media file %q: %w", path, err)
	}
	return nil
}

// MarkMediaFileMissing flags a file the scan could not confirm is gone —
// an unreachable mount rather than a deletion.
//
// missing_detected_at is set with COALESCE so the timestamp records when the
// file *first* went missing, not when the most recent scan noticed it again.
func (s *Store) MarkMediaFileMissing(ctx context.Context, libraryID, path string) error {
	if _, err := s.exec(ctx, `
		UPDATE media_files
		SET missing = 1,
		    missing_detected_at = COALESCE(missing_detected_at, CURRENT_TIMESTAMP),
		    updated_at = CURRENT_TIMESTAMP
		WHERE library_id = ? AND path = ?`, libraryID, path); err != nil {
		return fmt.Errorf("mark missing media file %q: %w", path, err)
	}
	return nil
}

// DeleteAudiobook removes an audiobook row.
func (s *Store) DeleteAudiobook(ctx context.Context, id string) error {
	if _, err := s.exec(ctx, `DELETE FROM audiobooks WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete stale audiobook %q: %w", id, err)
	}
	return nil
}

// DeletePodcast removes a podcast show row.
func (s *Store) DeletePodcast(ctx context.Context, id string) error {
	if _, err := s.exec(ctx, `DELETE FROM podcasts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete stale podcast %q: %w", id, err)
	}
	return nil
}

// TouchLibraryScanned stamps the library as scanned just now.
func (s *Store) TouchLibraryScanned(ctx context.Context, libraryID string) error {
	if _, err := s.exec(ctx, `UPDATE libraries SET last_scan_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, libraryID); err != nil {
		return fmt.Errorf("update library last_scan_at: %w", err)
	}
	return nil
}

// orphanMusicStatements delete music rows nothing references any more, in
// dependency order: tracks first, then the albums and artists those tracks
// were the last reference to.
//
// A track is kept when a playlist still names it, even with no media file
// behind it — a playlist entry pointing at a track that vanished mid-scan
// would otherwise be silently dropped from the playlist.
//
// track_ids_json is NOT NULL DEFAULT '[]', so NULLIF only guards against a bad
// write leaving an empty string there. Without it that one row would raise
// "invalid input syntax for type json" and fail the whole prune, not just skip
// the playlist — the same reasoning migration 0006 applied to json_extract.
var orphanMusicStatements = []string{
	`DELETE FROM music_tracks
		 WHERE id NOT IN (SELECT track_id FROM media_files WHERE track_id IS NOT NULL)
		   AND id NOT IN (
		     SELECT DISTINCT j.value
		     FROM music_playlists p, json_array_elements_text(NULLIF(p.track_ids_json, '')::json) AS j(value)
		     WHERE j.value IS NOT NULL AND TRIM(j.value) != ''
		   )`,
	`DELETE FROM music_albums
		 WHERE id NOT IN (SELECT album_id FROM music_tracks WHERE album_id IS NOT NULL)`,
	`DELETE FROM music_artists
		 WHERE id NOT IN (SELECT artist_id FROM music_track_artists)
		   AND id NOT IN (SELECT artist_id FROM music_album_artists)`,
}

// PruneOrphanMusic deletes music rows no longer referenced by any media file
// or playlist, and reports how many rows went.
func (s *Store) PruneOrphanMusic(ctx context.Context) (int, error) {
	pruned := 0
	for _, statement := range orphanMusicStatements {
		res, err := s.exec(ctx, statement)
		if err != nil {
			return pruned, fmt.Errorf("prune orphan music rows: %w", err)
		}
		if rows, err := res.RowsAffected(); err == nil {
			pruned += int(rows)
		}
	}
	return pruned, nil
}

// stringColumn runs a single-column query and collects it. op names the
// operation for the error the caller will see.
func (s *Store) stringColumn(ctx context.Context, op, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
