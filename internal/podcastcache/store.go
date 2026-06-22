package podcastcache

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

type cacheRow struct {
	EpisodeID    string
	EnclosureURL string
	CachePath    string
	ContentType  string
	SizeBytes    int64
	DownloadedAt string
	LastAccessed string
}

func loadCacheRow(ctx context.Context, db *sql.DB, episodeID string) (cacheRow, bool, error) {
	var row cacheRow
	err := db.QueryRowContext(ctx, `
		SELECT episode_id, enclosure_url, cache_path, content_type, size_bytes, downloaded_at, last_accessed_at
		FROM podcast_episode_cache
		WHERE episode_id = ?`, episodeID).
		Scan(&row.EpisodeID, &row.EnclosureURL, &row.CachePath, &row.ContentType, &row.SizeBytes, &row.DownloadedAt, &row.LastAccessed)
	if err == sql.ErrNoRows {
		return cacheRow{}, false, nil
	}
	if err != nil {
		return cacheRow{}, false, fmt.Errorf("load podcast cache row: %w", err)
	}
	return row, true, nil
}

func (s *Service) PruneOrphans(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT episode_id, cache_path
		FROM podcast_episode_cache
		WHERE episode_id NOT IN (SELECT id FROM podcast_episodes)`)
	if err != nil {
		return fmt.Errorf("list orphan podcast cache rows: %w", err)
	}
	stale, err := scanCachePaths(rows)
	if err != nil {
		return err
	}
	for _, row := range stale {
		if err := s.removeCacheRow(ctx, row.episodeID, row.cachePath); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) PruneRetention(ctx context.Context) error {
	if s == nil || s.db == nil || !s.enabled {
		return nil
	}
	if s.maxAge > 0 {
		cutoff := time.Now().UTC().Add(-s.maxAge).Format(time.RFC3339)
		if err := s.deleteRowsOlderThan(ctx, cutoff); err != nil {
			return err
		}
	}
	if s.maxBytes > 0 {
		return s.pruneToMaxBytes(ctx)
	}
	return nil
}

func (s *Service) deleteRowsOlderThan(ctx context.Context, cutoff string) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT episode_id, cache_path
		FROM podcast_episode_cache
		WHERE last_accessed_at < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("list expired podcast cache rows: %w", err)
	}
	expired, err := scanCachePaths(rows)
	if err != nil {
		return err
	}
	for _, row := range expired {
		if err := s.removeCacheRow(ctx, row.episodeID, row.cachePath); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) pruneToMaxBytes(ctx context.Context) error {
	maxBytes := s.CacheMaxBytes(ctx)
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes), 0) FROM podcast_episode_cache`).Scan(&total); err != nil {
		return fmt.Errorf("sum podcast cache size: %w", err)
	}
	if total <= maxBytes {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT episode_id, cache_path, size_bytes
		FROM podcast_episode_cache
		ORDER BY last_accessed_at ASC`)
	if err != nil {
		return fmt.Errorf("list podcast cache rows for pruning: %w", err)
	}
	candidates, err := scanCacheCandidates(rows)
	if err != nil {
		return err
	}
	for _, row := range candidates {
		if total <= maxBytes {
			break
		}
		if err := s.removeCacheRow(ctx, row.episodeID, row.cachePath); err != nil {
			return err
		}
		if row.sizeBytes > 0 {
			total -= row.sizeBytes
		}
	}
	return nil
}

func (s *Service) PruneStaleEnclosures(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.episode_id, c.cache_path
		FROM podcast_episode_cache c
		JOIN podcast_episodes e ON e.id = c.episode_id
		WHERE c.enclosure_url != e.enclosure_url`)
	if err != nil {
		return fmt.Errorf("list stale podcast cache rows: %w", err)
	}
	stale, err := scanCachePaths(rows)
	if err != nil {
		return err
	}
	for _, row := range stale {
		if err := s.removeCacheRow(ctx, row.episodeID, row.cachePath); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) PruneAfterFeedSave(ctx context.Context) error {
	if s == nil || !s.enabled {
		return nil
	}
	if err := s.PruneStaleEnclosures(ctx); err != nil {
		return err
	}
	if err := s.PruneOrphans(ctx); err != nil {
		return err
	}
	return s.PruneRetention(ctx)
}

func removeFileIfPresent(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

type cachePathRow struct {
	episodeID string
	cachePath string
}

type cacheCandidateRow struct {
	episodeID string
	cachePath string
	sizeBytes int64
}

func scanCachePaths(rows *sql.Rows) ([]cachePathRow, error) {
	defer rows.Close()
	var items []cachePathRow
	for rows.Next() {
		var row cachePathRow
		if err := rows.Scan(&row.episodeID, &row.cachePath); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

func scanCacheCandidates(rows *sql.Rows) ([]cacheCandidateRow, error) {
	defer rows.Close()
	var items []cacheCandidateRow
	for rows.Next() {
		var row cacheCandidateRow
		if err := rows.Scan(&row.episodeID, &row.cachePath, &row.sizeBytes); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}
