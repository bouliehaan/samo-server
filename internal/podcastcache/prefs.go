package podcastcache

import (
	"context"
	"sort"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// globalPrewarmScope is the podcast_prefs.show_id used for the library-wide
// default that applies to shows without an explicit override.
const globalPrewarmScope = ""

// PrewarmCount resolves the effective "keep newest N warm" count for a show:
// a per-show override, else the global override, else the env default.
func (s *Service) PrewarmCount(ctx context.Context, showID string) int {
	if s == nil || s.db == nil {
		return 0
	}
	if showID != globalPrewarmScope {
		if count, ok := s.lookupPrewarmCount(ctx, showID); ok {
			return count
		}
	}
	if count, ok := s.lookupPrewarmCount(ctx, globalPrewarmScope); ok {
		return count
	}
	return s.defaultPrewarmCount
}

// GetPrewarmCount returns the stored count for exactly this scope and whether a
// row exists (so the API can distinguish "explicitly 0" from "use the default").
func (s *Service) GetPrewarmCount(ctx context.Context, showID string) (int, bool) {
	if s == nil || s.db == nil {
		return 0, false
	}
	return s.lookupPrewarmCount(ctx, showID)
}

// DefaultPrewarmCount is the env-configured fallback count.
func (s *Service) DefaultPrewarmCount() int {
	if s == nil {
		return 0
	}
	return s.defaultPrewarmCount
}

func (s *Service) lookupPrewarmCount(ctx context.Context, showID string) (int, bool) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT prewarm_count FROM podcast_prefs WHERE show_id = ?`, showID).Scan(&count)
	if err != nil {
		return 0, false
	}
	if count < 0 {
		count = 0
	}
	return count, true
}

// SetPrewarmCount upserts the count for a scope (showID == "" sets the global
// default). Negative values are clamped to 0 (off).
func (s *Service) SetPrewarmCount(ctx context.Context, showID string, count int) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	if count < 0 {
		count = 0
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO podcast_prefs (show_id, prewarm_count, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(show_id) DO UPDATE SET
		  prewarm_count = excluded.prewarm_count,
		  updated_at = CURRENT_TIMESTAMP`,
		showID, count)
	return err
}

// CacheMaxBytes is the effective cache size cap: the app-set override if present
// and positive, otherwise the env/constructor default (Options.MaxBytes).
func (s *Service) CacheMaxBytes(ctx context.Context) int64 {
	if s == nil {
		return 0
	}
	if s.db != nil {
		var override int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT max_bytes FROM podcast_cache_settings WHERE id = 1`).Scan(&override); err == nil && override > 0 {
			return override
		}
	}
	return s.maxBytes
}

// SetCacheMaxBytes stores an app-set cache size cap (bytes) and immediately
// prunes to enforce it. A value <= 0 clears the override (revert to the default).
func (s *Service) SetCacheMaxBytes(ctx context.Context, maxBytes int64) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO podcast_cache_settings (id, max_bytes, updated_at)
		VALUES (1, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  max_bytes = excluded.max_bytes,
		  updated_at = CURRENT_TIMESTAMP`, maxBytes); err != nil {
		return err
	}
	return s.pruneToMaxBytes(ctx)
}

// PrewarmNewest ensures the newest `count` episodes of a show are warm in the
// on-disk cache. Fire-and-forget: selection is synchronous (cheap), downloads
// run in the background bounded by a generous timeout. EnsureCached is
// idempotent, so already-cached episodes are skipped and the byte cap pruning
// keeps total usage in check.
func (s *Service) PrewarmNewest(showID string, episodes []catalog.PodcastEpisode, count int) {
	if s == nil || !s.Enabled() || count <= 0 {
		return
	}
	targets := selectNewestEpisodes(episodes, count)
	if len(targets) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		for _, episode := range targets {
			if ctx.Err() != nil {
				return
			}
			_ = s.EnsureCached(ctx, episode)
		}
	}()
}

// selectNewestEpisodes returns the newest `count` episodes (by PublishedAt,
// newest first) that have a playable enclosure and aren't already local files.
func selectNewestEpisodes(episodes []catalog.PodcastEpisode, count int) []catalog.PodcastEpisode {
	if count <= 0 {
		return nil
	}
	candidates := make([]catalog.PodcastEpisode, 0, len(episodes))
	for _, episode := range episodes {
		if len(episode.AudioFiles) > 0 || episode.EnclosureURL == "" {
			continue
		}
		candidates = append(candidates, episode)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return episodePublishedUnix(candidates[i]) > episodePublishedUnix(candidates[j])
	})
	if len(candidates) > count {
		candidates = candidates[:count]
	}
	return candidates
}

func episodePublishedUnix(episode catalog.PodcastEpisode) int64 {
	if episode.PublishedAt != nil {
		return episode.PublishedAt.UnixNano()
	}
	return 0
}
