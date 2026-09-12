package scannerstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// PrunablePath is one indexed path and whether an earlier scan already flagged
// it missing.
type PrunablePath struct {
	Path    string
	Missing bool
}

// MediaFilePathsForPrune lists every path the library has indexed, the ones
// already flagged missing included.
//
// The missing rows are the point. Phase 2 flags every path the walk did not
// see so moved files can be paired with their new location by persistent id,
// which means that by the time prune runs, a genuinely deleted file is already
// missing = 1. A prune that only considered missing = 0 rows therefore never
// saw a deletion at all: the first scan after the delete flagged the row, and
// no later scan — quick or full — would look at it again. Deleted albums sat
// in the catalog forever.
func (s *Store) MediaFilePathsForPrune(ctx context.Context, libraryID string) ([]PrunablePath, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT path, missing FROM media_files WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("list media files for prune: %w", err)
	}
	defer rows.Close()

	var out []PrunablePath
	for rows.Next() {
		var item PrunablePath
		var missing int
		if err := rows.Scan(&item.Path, &missing); err != nil {
			return nil, fmt.Errorf("list media files for prune: %w", err)
		}
		item.Missing = missing != 0
		out = append(out, item)
	}
	return out, rows.Err()
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
// A track used to be kept when a playlist named it, even with no media file
// behind it, so a track that vanished mid-scan would not be dropped from the
// playlist. Flagging missing files covers that now — a file that disappears
// keeps its media_files row until somebody reviews it, so the track never
// looks orphaned in the first place. What the exception actually produced was
// permanent blank entries: one playlist reference kept a fileless track alive,
// and the track kept its album and artist alive with it, unplayable and
// unremovable from the library. Playlists give up their dead entries instead
// (see stripTracksFromPlaylists).
var orphanMusicStatements = []string{
	`DELETE FROM music_tracks
		 WHERE id NOT IN (SELECT track_id FROM media_files WHERE track_id IS NOT NULL)`,
	`DELETE FROM music_albums
		 WHERE id NOT IN (SELECT album_id FROM music_tracks WHERE album_id IS NOT NULL)`,
	`DELETE FROM music_artists
		 WHERE id NOT IN (SELECT artist_id FROM music_track_artists)
		   AND id NOT IN (SELECT artist_id FROM music_album_artists)`,
}

// PruneOrphanMusic deletes music rows no media file points at any more, drops
// those tracks from any playlist that named them, and reports how many rows
// went.
func (s *Store) PruneOrphanMusic(ctx context.Context) (int, error) {
	dead, err := s.stringColumn(ctx, "list fileless tracks",
		`SELECT id FROM music_tracks
		 WHERE id NOT IN (SELECT track_id FROM media_files WHERE track_id IS NOT NULL)`)
	if err != nil {
		return 0, err
	}
	// Strip first: the delete below would otherwise leave playlists naming ids
	// that no longer resolve to anything.
	if err := s.stripTracksFromPlaylists(ctx, dead); err != nil {
		return 0, err
	}

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

// stripTracksFromPlaylists removes the given track ids from every playlist that
// names them. Rewriting in Go rather than SQL keeps the json handling in one
// place and leaves the surviving order untouched.
func (s *Store) stripTracksFromPlaylists(ctx context.Context, trackIDs []string) error {
	if len(trackIDs) == 0 {
		return nil
	}
	drop := make(map[string]struct{}, len(trackIDs))
	for _, id := range trackIDs {
		drop[id] = struct{}{}
	}

	playlists, err := s.PlaylistTrackReferences(ctx)
	if err != nil {
		return err
	}
	for _, playlist := range playlists {
		ids := decodePlaylistTrackIDs(playlist.TrackIDsJSON)
		if len(ids) == 0 {
			continue
		}
		kept := make([]string, 0, len(ids))
		for _, id := range ids {
			if _, gone := drop[id]; gone {
				continue
			}
			kept = append(kept, id)
		}
		if len(kept) == len(ids) {
			continue
		}
		if err := s.SetPlaylistTrackIDs(ctx, playlist.ID, kept); err != nil {
			return err
		}
	}
	return nil
}

// decodePlaylistTrackIDs reads a playlist's track_ids_json. A row that will not
// parse is treated as empty rather than fatal: the column is NOT NULL DEFAULT
// '[]', so anything else took a bad write to produce, and one bad playlist must
// not fail the prune for every library.
func decodePlaylistTrackIDs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil
	}
	return ids
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
