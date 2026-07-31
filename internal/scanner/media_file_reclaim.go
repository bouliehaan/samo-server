package scanner

import (
	"context"
	"strings"

	"github.com/bouliehaan/samo-server/internal/scannerstore"
)

// audioFileOwner and mediaFileOwnerSnapshot were two structs with identical
// fields describing the same thing — which domain rows own a media file — one
// used for the owner being written, one for the owner read back before the
// write. They are the same shape because they are compared against each other.
type (
	audioFileOwner         = scannerstore.MediaFileOwner
	mediaFileOwnerSnapshot = scannerstore.MediaFileOwner
)

// mediaFileIDByPath resolves the row already indexed at path, or "" when the
// path is blank or unknown.
func (s *Scanner) mediaFileIDByPath(ctx context.Context, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	return s.store.MediaFileIDByPath(ctx, path)
}

// mediaFileOwners reads back the owners a file currently has, so a write can
// tell what it displaced.
func (s *Scanner) mediaFileOwners(ctx context.Context, fileID string) (mediaFileOwnerSnapshot, error) {
	if fileID == "" {
		return mediaFileOwnerSnapshot{}, nil
	}
	return s.store.MediaFileOwners(ctx, fileID)
}

// cleanupReplacedMediaOwners deletes an owner the file no longer belongs to,
// once nothing else references it.
//
// Only a *dropped* owner is considered: a file that moved from track A to
// track B leaves A possibly orphaned, but a file that simply gained a track it
// did not have before displaces nothing.
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
	refs, err := s.store.CountMediaFilesForTrack(ctx, trackID)
	if err != nil {
		return err
	}
	if refs > 0 {
		return nil
	}
	return s.store.DeleteMusicTrack(ctx, trackID)
}

func (s *Scanner) deletePodcastEpisodeIfOrphan(ctx context.Context, episodeID string) error {
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return nil
	}
	refs, err := s.store.CountMediaFilesForEpisode(ctx, episodeID)
	if err != nil {
		return err
	}
	if refs > 0 {
		return nil
	}
	return s.store.DeletePodcastEpisode(ctx, episodeID)
}
