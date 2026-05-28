package sources

import (
	"context"
	"fmt"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func (s *Service) loadPodcastEpisodeIDs(ctx context.Context, podcastID string) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM podcast_episodes WHERE podcast_id = ?`, podcastID)
	if err != nil {
		return nil, fmt.Errorf("list podcast episode ids: %w", err)
	}
	defer rows.Close()

	ids := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = struct{}{}
	}
	return ids, rows.Err()
}

func (s *Service) prefetchPodcastEpisodes(episodes []catalog.PodcastEpisode) {
	if s == nil || s.podcastCache == nil || !s.podcastCache.Enabled() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		for _, episode := range episodes {
			if len(episode.AudioFiles) > 0 || episode.EnclosureURL == "" {
				continue
			}
			_ = s.podcastCache.EnsureCached(ctx, episode)
		}
	}()
}
