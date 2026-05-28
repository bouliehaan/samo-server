package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrRemoteItem = errors.New("remote items must be removed via feed delete")

type DeleteOptions struct {
	DeleteFiles bool
}

type DeleteResult struct {
	ID           string   `json:"id"`
	FilesRemoved int      `json:"filesRemoved"`
	FileErrors   []string `json:"fileErrors,omitempty"`
}

func DeleteMusicAlbum(ctx context.Context, db *sql.DB, albumID string, opts DeleteOptions) (DeleteResult, error) {
	albumID = strings.TrimSpace(albumID)
	if albumID == "" {
		return DeleteResult{}, ErrNotFound
	}
	if db == nil {
		return DeleteResult{}, errors.New("nil database")
	}

	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_albums WHERE id = ?`, albumID).Scan(&exists); err != nil {
		return DeleteResult{}, fmt.Errorf("lookup album: %w", err)
	}
	if exists == 0 {
		return DeleteResult{}, ErrNotFound
	}

	trackIDs, err := queryStringColumn(ctx, db, `SELECT id FROM music_tracks WHERE album_id = ?`, albumID)
	if err != nil {
		return DeleteResult{}, err
	}
	paths, err := mediaFilePathsForTracks(ctx, db, trackIDs)
	if err != nil {
		return DeleteResult{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return DeleteResult{}, err
	}
	defer tx.Rollback()

	if err := deleteUserPlaybackTargets(ctx, tx, OverrideKindMusicAlbum, albumID); err != nil {
		return DeleteResult{}, err
	}
	if err := deleteUserPlaybackTargets(ctx, tx, OverrideKindMusicTrack, trackIDs...); err != nil {
		return DeleteResult{}, err
	}
	if err := deleteMetadataOverrides(ctx, tx, OverrideKindMusicAlbum, albumID); err != nil {
		return DeleteResult{}, err
	}
	if err := deleteMetadataOverrides(ctx, tx, OverrideKindMusicTrack, trackIDs...); err != nil {
		return DeleteResult{}, err
	}
	if err := removeTracksFromPlaylists(ctx, tx, trackIDs); err != nil {
		return DeleteResult{}, err
	}
	if err := deleteRadioItemsForSources(ctx, tx, "music-track", trackIDs...); err != nil {
		return DeleteResult{}, err
	}
	if len(trackIDs) > 0 {
		query, args := inClause(`DELETE FROM media_files WHERE track_id IN (`, trackIDs)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return DeleteResult{}, fmt.Errorf("delete album media files: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM music_tracks WHERE album_id = ?`, albumID); err != nil {
			return DeleteResult{}, fmt.Errorf("delete album tracks: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM music_albums WHERE id = ?`, albumID); err != nil {
		return DeleteResult{}, fmt.Errorf("delete album: %w", err)
	}
	if err := pruneOrphanMusic(ctx, tx); err != nil {
		return DeleteResult{}, err
	}
	if err := refreshAggregateStats(ctx, tx); err != nil {
		return DeleteResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeleteResult{}, err
	}

	filesRemoved, fileErrors, err := deletePathsIfRequested(ctx, db, paths, opts.DeleteFiles)
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{ID: albumID, FilesRemoved: filesRemoved, FileErrors: fileErrors}, nil
}

func DeleteAudiobook(ctx context.Context, db *sql.DB, audiobookID string, opts DeleteOptions) (DeleteResult, error) {
	audiobookID = strings.TrimSpace(audiobookID)
	if audiobookID == "" {
		return DeleteResult{}, ErrNotFound
	}
	if db == nil {
		return DeleteResult{}, errors.New("nil database")
	}

	libraryPath, err := libraryPathForAudiobook(ctx, db, audiobookID)
	if err != nil {
		return DeleteResult{}, err
	}
	if strings.HasPrefix(libraryPath, "samo://") {
		return DeleteResult{}, ErrRemoteItem
	}

	paths, err := queryStringColumn(ctx, db, `SELECT path FROM media_files WHERE audiobook_id = ?`, audiobookID)
	if err != nil {
		return DeleteResult{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return DeleteResult{}, err
	}
	defer tx.Rollback()

	if err := deleteUserPlaybackTargets(ctx, tx, OverrideKindAudiobook, audiobookID); err != nil {
		return DeleteResult{}, err
	}
	if err := deleteMetadataOverrides(ctx, tx, OverrideKindAudiobook, audiobookID); err != nil {
		return DeleteResult{}, err
	}
	if err := deleteRadioItemsForSources(ctx, tx, "audiobook", audiobookID); err != nil {
		return DeleteResult{}, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM audiobooks WHERE id = ?`, audiobookID)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("delete audiobook: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return DeleteResult{}, ErrNotFound
	}
	if err := refreshAggregateStats(ctx, tx); err != nil {
		return DeleteResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeleteResult{}, err
	}

	filesRemoved, fileErrors, err := deletePathsIfRequested(ctx, db, paths, opts.DeleteFiles)
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{ID: audiobookID, FilesRemoved: filesRemoved, FileErrors: fileErrors}, nil
}

func DeletePodcastShow(ctx context.Context, db *sql.DB, podcastID string, opts DeleteOptions) (DeleteResult, error) {
	podcastID = strings.TrimSpace(podcastID)
	if podcastID == "" {
		return DeleteResult{}, ErrNotFound
	}
	if db == nil {
		return DeleteResult{}, errors.New("nil database")
	}

	if err := ensureFilesystemPodcast(ctx, db, podcastID); err != nil {
		return DeleteResult{}, err
	}

	episodeIDs, err := queryStringColumn(ctx, db, `SELECT id FROM podcast_episodes WHERE podcast_id = ?`, podcastID)
	if err != nil {
		return DeleteResult{}, err
	}
	paths, err := queryStringColumn(ctx, db, `SELECT path FROM media_files WHERE podcast_id = ?`, podcastID)
	if err != nil {
		return DeleteResult{}, err
	}
	if len(episodeIDs) > 0 {
		query, args := inClause(`SELECT path FROM media_files WHERE episode_id IN (`, episodeIDs)
		episodePaths, err := queryStringColumn(ctx, db, query, args...)
		if err != nil {
			return DeleteResult{}, err
		}
		paths = uniqueStrings(append(paths, episodePaths...))
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return DeleteResult{}, err
	}
	defer tx.Rollback()

	if err := deleteUserPlaybackTargets(ctx, tx, OverrideKindPodcast, podcastID); err != nil {
		return DeleteResult{}, err
	}
	if err := deleteUserPlaybackTargets(ctx, tx, OverrideKindPodcastEpisode, episodeIDs...); err != nil {
		return DeleteResult{}, err
	}
	if err := deleteMetadataOverrides(ctx, tx, OverrideKindPodcast, podcastID); err != nil {
		return DeleteResult{}, err
	}
	if err := deleteMetadataOverrides(ctx, tx, OverrideKindPodcastEpisode, episodeIDs...); err != nil {
		return DeleteResult{}, err
	}
	if err := deleteRadioItemsForSources(ctx, tx, "podcast-episode", episodeIDs...); err != nil {
		return DeleteResult{}, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM podcasts WHERE id = ?`, podcastID)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("delete podcast show: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return DeleteResult{}, ErrNotFound
	}
	if err := refreshAggregateStats(ctx, tx); err != nil {
		return DeleteResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeleteResult{}, err
	}

	filesRemoved, fileErrors, err := deletePathsIfRequested(ctx, db, paths, opts.DeleteFiles)
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{ID: podcastID, FilesRemoved: filesRemoved, FileErrors: fileErrors}, nil
}

func ensureFilesystemPodcast(ctx context.Context, db *sql.DB, podcastID string) error {
	var libraryPath string
	err := db.QueryRowContext(ctx, `
		SELECT l.path
		FROM podcasts p
		JOIN libraries l ON l.id = p.library_id
		WHERE p.id = ?`, podcastID).Scan(&libraryPath)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lookup podcast library: %w", err)
	}
	if strings.HasPrefix(strings.TrimSpace(libraryPath), "samo://") {
		return ErrRemoteItem
	}
	var feedCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM podcast_feeds WHERE podcast_id = ?`, podcastID).Scan(&feedCount); err != nil {
		return fmt.Errorf("lookup podcast feed: %w", err)
	}
	if feedCount > 0 {
		return ErrRemoteItem
	}
	return nil
}

func libraryPathForAudiobook(ctx context.Context, db *sql.DB, audiobookID string) (string, error) {
	var libraryPath string
	err := db.QueryRowContext(ctx, `
		SELECT l.path
		FROM audiobooks a
		JOIN libraries l ON l.id = a.library_id
		WHERE a.id = ?`, audiobookID).Scan(&libraryPath)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lookup audiobook library: %w", err)
	}
	return libraryPath, nil
}

func mediaFilePathsForTracks(ctx context.Context, db *sql.DB, trackIDs []string) ([]string, error) {
	if len(trackIDs) == 0 {
		return nil, nil
	}
	query, args := inClause(`SELECT path FROM media_files WHERE track_id IN (`, trackIDs)
	return queryStringColumn(ctx, db, query, args...)
}

func deleteUserPlaybackTargets(ctx context.Context, tx *sql.Tx, kind string, ids ...string) error {
	ids = nonEmptyStrings(ids)
	if len(ids) == 0 {
		return nil
	}
	args := append([]any{kind}, stringArgs(ids)...)
	query := `DELETE FROM user_playback WHERE target_kind = ? AND target_id IN (` + placeholders(len(ids)) + `)`
	_, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("delete user playback for %s: %w", kind, err)
	}
	return nil
}

func deleteMetadataOverrides(ctx context.Context, tx *sql.Tx, kind string, ids ...string) error {
	for _, id := range nonEmptyStrings(ids) {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM metadata_overrides
			WHERE target_kind = ? AND target_id = ?`, kind, id); err != nil {
			return fmt.Errorf("delete metadata override %s/%s: %w", kind, id, err)
		}
	}
	return nil
}

func deleteRadioItemsForSources(ctx context.Context, tx *sql.Tx, kind string, ids ...string) error {
	ids = nonEmptyStrings(ids)
	if len(ids) == 0 {
		return nil
	}
	args := append([]any{kind}, stringArgs(ids)...)
	query := `DELETE FROM radio_station_items WHERE source_kind = ? AND source_id IN (` + placeholders(len(ids)) + `)`
	_, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("delete radio items for %s: %w", kind, err)
	}
	return nil
}

func removeTracksFromPlaylists(ctx context.Context, tx *sql.Tx, trackIDs []string) error {
	if len(trackIDs) == 0 {
		return nil
	}
	remove := map[string]struct{}{}
	for _, id := range trackIDs {
		remove[id] = struct{}{}
	}

	rows, err := tx.QueryContext(ctx, `SELECT id, track_ids_json FROM music_playlists`)
	if err != nil {
		return fmt.Errorf("list playlists: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var playlistID, trackIDsJSON string
		if err := rows.Scan(&playlistID, &trackIDsJSON); err != nil {
			return fmt.Errorf("scan playlist: %w", err)
		}
		var current []string
		if strings.TrimSpace(trackIDsJSON) != "" {
			if err := json.Unmarshal([]byte(trackIDsJSON), &current); err != nil {
				return fmt.Errorf("decode playlist %q tracks: %w", playlistID, err)
			}
		}
		next := current[:0]
		changed := false
		for _, trackID := range current {
			if _, drop := remove[trackID]; drop {
				changed = true
				continue
			}
			next = append(next, trackID)
		}
		if !changed {
			continue
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return fmt.Errorf("encode playlist %q tracks: %w", playlistID, err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE music_playlists
			SET track_ids_json = ?, track_count = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`, string(encoded), len(next), playlistID); err != nil {
			return fmt.Errorf("update playlist %q tracks: %w", playlistID, err)
		}
	}
	return rows.Err()
}

func pruneOrphanMusic(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`DELETE FROM music_tracks
		 WHERE id NOT IN (SELECT track_id FROM media_files WHERE track_id IS NOT NULL)`,
		`DELETE FROM music_albums
		 WHERE id NOT IN (SELECT album_id FROM music_tracks WHERE album_id IS NOT NULL)`,
		`DELETE FROM music_artists
		 WHERE id NOT IN (SELECT artist_id FROM music_track_artists)
		   AND id NOT IN (SELECT artist_id FROM music_album_artists)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prune orphan music rows: %w", err)
		}
	}
	return nil
}

func refreshAggregateStats(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`UPDATE music_albums
		 SET track_count = (SELECT COUNT(*) FROM music_tracks WHERE album_id = music_albums.id),
		     duration_seconds = COALESCE((SELECT SUM(duration_seconds) FROM music_tracks WHERE album_id = music_albums.id), 0),
		     disc_count = COALESCE((SELECT MAX(disc_number) FROM music_tracks WHERE album_id = music_albums.id), 0)`,
		`UPDATE music_artists
		 SET track_count = COALESCE((SELECT COUNT(DISTINCT track_id) FROM music_track_artists WHERE artist_id = music_artists.id), 0),
		     album_count = COALESCE((SELECT COUNT(DISTINCT album_id) FROM music_album_artists WHERE artist_id = music_artists.id), 0),
		     duration_seconds = COALESCE((
		       SELECT SUM(t.duration_seconds)
		       FROM music_tracks t
		       JOIN music_track_artists ta ON ta.track_id = t.id
		       WHERE ta.artist_id = music_artists.id
		     ), 0)`,
		`UPDATE audiobooks
		 SET duration_seconds = COALESCE((SELECT SUM(duration_seconds) FROM media_files WHERE audiobook_id = audiobooks.id), duration_seconds)`,
		`UPDATE podcasts
		 SET duration_seconds = COALESCE((SELECT SUM(duration_seconds) FROM media_files WHERE podcast_id = podcasts.id), duration_seconds)`,
		`UPDATE contributors
		 SET item_count = COALESCE((SELECT COUNT(DISTINCT audiobook_id) FROM audiobook_contributors WHERE contributor_id = contributors.id), 0),
		     duration_seconds = COALESCE((
		       SELECT SUM(a.duration_seconds)
		       FROM audiobooks a
		       JOIN audiobook_contributors ac ON ac.audiobook_id = a.id
		       WHERE ac.contributor_id = contributors.id
		     ), 0)`,
		`UPDATE series
		 SET item_count = COALESCE((SELECT COUNT(DISTINCT audiobook_id) FROM audiobook_series WHERE series_id = series.id), 0),
		     duration_seconds = COALESCE((
		       SELECT SUM(a.duration_seconds)
		       FROM audiobooks a
		       JOIN audiobook_series aas ON aas.audiobook_id = a.id
		       WHERE aas.series_id = series.id
		     ), 0)`,
		`UPDATE libraries
		 SET item_count = CASE
		   WHEN kind = 'music' THEN COALESCE((
		     SELECT COUNT(DISTINCT t.id)
		     FROM music_tracks t
		     JOIN media_files mf ON mf.track_id = t.id
		     WHERE mf.library_id = libraries.id
		   ), 0)
		   WHEN kind = 'audiobook' THEN COALESCE((SELECT COUNT(*) FROM audiobooks WHERE library_id = libraries.id), 0)
		   WHEN kind = 'podcast' THEN COALESCE((SELECT COUNT(*) FROM podcasts WHERE library_id = libraries.id), 0)
		   WHEN kind = 'mixed' THEN COALESCE((
		     SELECT COUNT(DISTINCT t.id)
		     FROM music_tracks t
		     JOIN media_files mf ON mf.track_id = t.id
		     WHERE mf.library_id = libraries.id
		   ), 0)
		     + COALESCE((SELECT COUNT(*) FROM audiobooks WHERE library_id = libraries.id), 0)
		     + COALESCE((SELECT COUNT(*) FROM podcasts WHERE library_id = libraries.id), 0)
		   ELSE item_count
		 END`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("refresh aggregate stats: %w", err)
		}
	}
	return nil
}

func deletePathsIfRequested(ctx context.Context, db *sql.DB, paths []string, deleteFiles bool) (int, []string, error) {
	if !deleteFiles {
		return 0, nil, nil
	}
	paths = uniqueStrings(paths)
	if len(paths) == 0 {
		return 0, nil, nil
	}
	roots, err := loadLibraryRoots(ctx, db)
	if err != nil {
		return 0, nil, err
	}
	removed := 0
	var fileErrors []string
	for _, path := range paths {
		if !pathUnderRoots(path, roots) {
			continue
		}
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			fileErrors = append(fileErrors, fmt.Sprintf("delete file %q: %v", path, err))
			continue
		}
		removed++
	}
	return removed, fileErrors, nil
}

func loadLibraryRoots(ctx context.Context, db *sql.DB) ([]string, error) {
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
		roots = append(roots, filepath.Clean(absolute))
	}
	return roots, rows.Err()
}

func pathUnderRoots(path string, roots []string) bool {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return false
	}
	clean := filepath.Clean(absolute)
	for _, root := range roots {
		if clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func queryStringColumn(ctx context.Context, db queryExecer, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

type queryExecer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func inClause(prefix string, ids []string) (string, []any) {
	if len(ids) == 0 {
		return prefix + "NULL)", nil
	}
	query := prefix + placeholders(len(ids)) + ")"
	args := stringArgs(ids)
	return query, args
}

func placeholders(count int) string {
	if count <= 0 {
		return "NULL"
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ", ")
}

func stringArgs(values []string) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
