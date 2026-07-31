package scannerstore

import (
	"context"
	"fmt"
)

// TrackPIDLink is a media file that carries a persistent track id, and the
// track it currently points at.
type TrackPIDLink struct {
	FileID    string
	LibraryID string
	TrackID   string
	TrackPID  string
}

// OrphanAudiobookFile is a media file whose audiobook_id names a book that no
// longer exists.
type OrphanAudiobookFile struct {
	FileID      string
	LibraryID   string
	Path        string
	AudiobookID string
	LibraryRoot string
}

// OrphanPodcastFile is a media file whose episode_id names an episode that no
// longer exists.
type OrphanPodcastFile struct {
	FileID       string
	LibraryID    string
	Path         string
	RelativePath string
	EpisodeID    string
	PodcastID    string
	LibraryRoot  string
}

// MediaFilesWithTrackPID lists every music file carrying a persistent track id.
//
// The whole result set is materialized before the caller acts on it. The
// caller's next move is to UPDATE these same rows, and issuing those writes
// while a cursor is still open over the table means holding that cursor for
// the length of the entire reconciliation.
func (s *Store) MediaFilesWithTrackPID(ctx context.Context) ([]TrackPIDLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, library_id, track_id, track_pid
		FROM media_files
		WHERE TRIM(COALESCE(track_pid, '')) != ''`)
	if err != nil {
		return nil, fmt.Errorf("list media files for track relink: %w", err)
	}
	defer rows.Close()

	var links []TrackPIDLink
	for rows.Next() {
		var link TrackPIDLink
		if err := rows.Scan(&link.FileID, &link.LibraryID, &link.TrackID, &link.TrackPID); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

// SetMediaFileTrack repoints a media file at a track.
func (s *Store) SetMediaFileTrack(ctx context.Context, fileID, trackID string) error {
	if _, err := s.exec(ctx, `
		UPDATE media_files
		SET track_id = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, trackID, fileID); err != nil {
		return fmt.Errorf("relink media file %q to track %q: %w", fileID, trackID, err)
	}
	return nil
}

// OrphanAudiobookFiles lists media files pointing at a deleted audiobook,
// joined to their library root so the caller can re-derive the owner from the
// path.
func (s *Store) OrphanAudiobookFiles(ctx context.Context) ([]OrphanAudiobookFile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mf.id, mf.library_id, mf.path, mf.audiobook_id, l.path
		FROM media_files mf
		JOIN libraries l ON l.id = mf.library_id
		WHERE mf.audiobook_id IS NOT NULL
		  AND TRIM(mf.audiobook_id) != ''
		  AND NOT EXISTS (SELECT 1 FROM audiobooks a WHERE a.id = mf.audiobook_id)`)
	if err != nil {
		return nil, fmt.Errorf("list orphan audiobook media files: %w", err)
	}
	defer rows.Close()

	var orphans []OrphanAudiobookFile
	for rows.Next() {
		var o OrphanAudiobookFile
		if err := rows.Scan(&o.FileID, &o.LibraryID, &o.Path, &o.AudiobookID, &o.LibraryRoot); err != nil {
			return nil, err
		}
		orphans = append(orphans, o)
	}
	return orphans, rows.Err()
}

// SetMediaFileAudiobook repoints a media file at an audiobook.
func (s *Store) SetMediaFileAudiobook(ctx context.Context, fileID, audiobookID string) error {
	if _, err := s.exec(ctx, `
		UPDATE media_files
		SET audiobook_id = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, audiobookID, fileID); err != nil {
		return fmt.Errorf("relink audiobook media file %q: %w", fileID, err)
	}
	return nil
}

// OrphanPodcastFiles lists media files pointing at a deleted episode.
func (s *Store) OrphanPodcastFiles(ctx context.Context) ([]OrphanPodcastFile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mf.id, mf.library_id, mf.path, mf.relative_path, mf.episode_id, mf.podcast_id, l.path
		FROM media_files mf
		JOIN libraries l ON l.id = mf.library_id
		WHERE mf.episode_id IS NOT NULL
		  AND TRIM(mf.episode_id) != ''
		  AND NOT EXISTS (SELECT 1 FROM podcast_episodes e WHERE e.id = mf.episode_id)`)
	if err != nil {
		return nil, fmt.Errorf("list orphan podcast media files: %w", err)
	}
	defer rows.Close()

	var orphans []OrphanPodcastFile
	for rows.Next() {
		var o OrphanPodcastFile
		if err := rows.Scan(&o.FileID, &o.LibraryID, &o.Path, &o.RelativePath, &o.EpisodeID, &o.PodcastID, &o.LibraryRoot); err != nil {
			return nil, err
		}
		orphans = append(orphans, o)
	}
	return orphans, rows.Err()
}

// SetMediaFilePodcastOwners repoints a media file at an episode and its show.
func (s *Store) SetMediaFilePodcastOwners(ctx context.Context, fileID, episodeID, podcastID string) error {
	if _, err := s.exec(ctx, `
		UPDATE media_files
		SET episode_id = ?,
		    podcast_id = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		nullableString(episodeID), nullableString(podcastID), fileID); err != nil {
		return fmt.Errorf("relink podcast media file %q: %w", fileID, err)
	}
	return nil
}

// MusicTrackExists reports whether a track row is present.
func (s *Store) MusicTrackExists(ctx context.Context, id string) bool {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM music_tracks WHERE id = ? LIMIT 1`, id).Scan(&exists); err != nil {
		return false
	}
	return exists == 1
}

// AudiobookExists reports whether an audiobook row is present.
func (s *Store) AudiobookExists(ctx context.Context, id string) bool {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM audiobooks WHERE id = ? LIMIT 1`, id).Scan(&exists); err != nil {
		return false
	}
	return exists == 1
}

// PodcastEpisodeExists reports whether an episode row is present.
func (s *Store) PodcastEpisodeExists(ctx context.Context, id string) bool {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM podcast_episodes WHERE id = ? LIMIT 1`, id).Scan(&exists); err != nil {
		return false
	}
	return exists == 1
}
