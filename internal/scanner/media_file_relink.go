package scanner

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
)

// reconcileMediaFileTrackLinks reattaches music media_files rows to the
// music_tracks row implied by track_pid. Quick scans and track-id migrations
// can leave stale track_id values; catalog then serves tracks with no
// audioFiles and streaming returns "no audio files available".
func (s *Scanner) reconcileMediaFileTrackLinks(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, library_id, track_id, track_pid
		FROM media_files
		WHERE TRIM(COALESCE(track_pid, '')) != ''`)
	if err != nil {
		return 0, fmt.Errorf("list media files for track relink: %w", err)
	}
	defer rows.Close()

	updated := 0
	for rows.Next() {
		var fileID, libraryID, trackID, trackPID string
		if err := rows.Scan(&fileID, &libraryID, &trackID, &trackPID); err != nil {
			return updated, err
		}
		want := stableID("track", libraryID, trackPID)
		if want == strings.TrimSpace(trackID) && s.trackIDExists(ctx, want) {
			continue
		}
		if !s.trackIDExists(ctx, want) {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE media_files
			SET track_id = ?,
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`, want, fileID); err != nil {
			return updated, fmt.Errorf("relink media file %q to track %q: %w", fileID, want, err)
		}
		if old := strings.TrimSpace(trackID); old != "" && old != want {
			s.noteTrackIDMigration(old, want)
		}
		updated++
	}
	if err := rows.Err(); err != nil {
		return updated, err
	}
	if updated > 0 {
		log.Printf("scanner: relinked track_id on %d media file(s)", updated)
	}
	return updated, nil
}

// reconcileLongformMediaOwners fixes audiobook_id / episode_id when the
// catalog row still exists but media_files point at a deleted owner id.
func (s *Scanner) reconcileLongformMediaOwners(ctx context.Context) (int, error) {
	updated := 0
	n, err := s.reconcileAudiobookMediaOwners(ctx)
	if err != nil {
		return updated, err
	}
	updated += n
	n, err = s.reconcilePodcastMediaOwners(ctx)
	if err != nil {
		return updated, err
	}
	updated += n
	if updated > 0 {
		log.Printf("scanner: relinked longform owner on %d media file(s)", updated)
	}
	return updated, nil
}

func (s *Scanner) reconcileAudiobookMediaOwners(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mf.id, mf.library_id, mf.path, mf.audiobook_id, l.path
		FROM media_files mf
		JOIN libraries l ON l.id = mf.library_id
		WHERE mf.audiobook_id IS NOT NULL
		  AND TRIM(mf.audiobook_id) != ''
		  AND NOT EXISTS (SELECT 1 FROM audiobooks a WHERE a.id = mf.audiobook_id)`)
	if err != nil {
		return 0, fmt.Errorf("list orphan audiobook media files: %w", err)
	}
	defer rows.Close()

	updated := 0
	for rows.Next() {
		var fileID, libraryID, filePath, oldOwner, libraryRoot string
		if err := rows.Scan(&fileID, &libraryID, &filePath, &oldOwner, &libraryRoot); err != nil {
			return updated, err
		}
		want, ok := s.audiobookIDForMediaPath(ctx, libraryID, libraryRoot, filePath)
		if !ok || want == oldOwner {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE media_files
			SET audiobook_id = ?,
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`, want, fileID); err != nil {
			return updated, fmt.Errorf("relink audiobook media file %q: %w", fileID, err)
		}
		updated++
	}
	return updated, rows.Err()
}

func (s *Scanner) audiobookIDForMediaPath(ctx context.Context, libraryID, libraryRoot, filePath string) (string, bool) {
	libraryRoot = strings.TrimSpace(libraryRoot)
	filePath = strings.TrimSpace(filePath)
	if libraryRoot == "" || filePath == "" {
		return "", false
	}
	groups := splitAudiobookGroups(groupAudiobooks(libraryRoot, []string{filePath}))
	if len(groups) == 0 {
		return "", false
	}
	want := stableID("audiobook", libraryID, groups[0].Root)
	return want, s.audiobookIDExists(ctx, want)
}

func (s *Scanner) audiobookIDExists(ctx context.Context, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM audiobooks WHERE id = ? LIMIT 1`, id).Scan(&exists); err != nil {
		return false
	}
	return exists == 1
}

func (s *Scanner) reconcilePodcastMediaOwners(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mf.id, mf.library_id, mf.path, mf.relative_path, mf.episode_id, mf.podcast_id, l.path
		FROM media_files mf
		JOIN libraries l ON l.id = mf.library_id
		WHERE mf.episode_id IS NOT NULL
		  AND TRIM(mf.episode_id) != ''
		  AND NOT EXISTS (SELECT 1 FROM podcast_episodes e WHERE e.id = mf.episode_id)`)
	if err != nil {
		return 0, fmt.Errorf("list orphan podcast media files: %w", err)
	}
	defer rows.Close()

	updated := 0
	for rows.Next() {
		var fileID, libraryID, filePath, relPath, oldEpisode, oldPodcast, libraryRoot string
		if err := rows.Scan(&fileID, &libraryID, &filePath, &relPath, &oldEpisode, &oldPodcast, &libraryRoot); err != nil {
			return updated, err
		}
		wantEpisode, wantPodcast, ok := s.podcastOwnersForMediaPath(ctx, libraryID, libraryRoot, filePath, relPath)
		if !ok {
			continue
		}
		if wantEpisode == oldEpisode && wantPodcast == oldPodcast {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE media_files
			SET episode_id = ?,
			    podcast_id = ?,
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			nullableString(wantEpisode), nullableString(wantPodcast), fileID); err != nil {
			return updated, fmt.Errorf("relink podcast media file %q: %w", fileID, err)
		}
		updated++
	}
	return updated, rows.Err()
}

func (s *Scanner) podcastOwnersForMediaPath(ctx context.Context, libraryID, libraryRoot, filePath, relPath string) (episodeID, podcastID string, ok bool) {
	libraryRoot = strings.TrimSpace(libraryRoot)
	filePath = strings.TrimSpace(filePath)
	if libraryRoot == "" || filePath == "" {
		return "", "", false
	}
	groups := groupPodcasts(libraryRoot, []string{filePath})
	if len(groups) == 0 {
		return "", "", false
	}
	podcastID = stableID("podcast", libraryID, groups[0].Root)
	if relPath == "" {
		relPath, _ = filepath.Rel(libraryRoot, filePath)
	}
	episodeID = stableID("episode", podcastID, relPath)
	if !s.episodeIDExists(ctx, episodeID) {
		return "", "", false
	}
	return episodeID, podcastID, true
}

func (s *Scanner) episodeIDExists(ctx context.Context, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM podcast_episodes WHERE id = ? LIMIT 1`, id).Scan(&exists); err != nil {
		return false
	}
	return exists == 1
}

func (s *Scanner) reconcileMediaFileOwners(ctx context.Context) error {
	if _, err := s.reconcileMediaFileTrackLinks(ctx); err != nil {
		return err
	}
	if _, err := s.reconcileLongformMediaOwners(ctx); err != nil {
		return err
	}
	return nil
}
