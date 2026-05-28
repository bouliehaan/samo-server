package scanner

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type mediaFileOwnerSnapshot struct {
	TrackID     string
	AudiobookID string
	PodcastID   string
	EpisodeID   string
}

func (s *Scanner) mediaFileIDByPath(ctx context.Context, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM media_files WHERE path = ?`, path).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Scanner) mediaFileOwners(ctx context.Context, fileID string) (mediaFileOwnerSnapshot, error) {
	var snap mediaFileOwnerSnapshot
	if fileID == "" {
		return snap, nil
	}
	var trackID, audiobookID, podcastID, episodeID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT track_id, audiobook_id, podcast_id, episode_id
		FROM media_files WHERE id = ?`, fileID).Scan(&trackID, &audiobookID, &podcastID, &episodeID)
	if err == sql.ErrNoRows {
		return snap, nil
	}
	if err != nil {
		return snap, err
	}
	snap.TrackID = trackID.String
	snap.AudiobookID = audiobookID.String
	snap.PodcastID = podcastID.String
	snap.EpisodeID = episodeID.String
	return snap, nil
}

func (s *Scanner) cleanupReplacedMediaOwners(ctx context.Context, before mediaFileOwnerSnapshot, owner audioFileOwner) error {
	if before.TrackID != "" && owner.TrackID == "" {
		if err := s.deleteMusicTrackIfOrphan(ctx, before.TrackID); err != nil {
			return err
		}
	}
	if before.EpisodeID != "" && owner.EpisodeID == "" {
		if err := s.deletePodcastEpisodeIfOrphan(ctx, before.EpisodeID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scanner) deleteMusicTrackIfOrphan(ctx context.Context, trackID string) error {
	trackID = strings.TrimSpace(trackID)
	if trackID == "" {
		return nil
	}
	var refs int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_files WHERE track_id = ?`, trackID).Scan(&refs); err != nil {
		return fmt.Errorf("count media_files for track %q: %w", trackID, err)
	}
	if refs > 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM music_tracks WHERE id = ?`, trackID); err != nil {
		return fmt.Errorf("delete orphan track %q: %w", trackID, err)
	}
	return nil
}

func (s *Scanner) deletePodcastEpisodeIfOrphan(ctx context.Context, episodeID string) error {
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return nil
	}
	var refs int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_files WHERE episode_id = ?`, episodeID).Scan(&refs); err != nil {
		return fmt.Errorf("count media_files for episode %q: %w", episodeID, err)
	}
	if refs > 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM podcast_episodes WHERE id = ?`, episodeID); err != nil {
		return fmt.Errorf("delete orphan episode %q: %w", episodeID, err)
	}
	return nil
}
