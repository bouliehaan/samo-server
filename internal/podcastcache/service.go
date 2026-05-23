package podcastcache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/podcaststream"
)

var (
	ErrDisabled     = errors.New("podcast cache service is disabled")
	ErrNotCached    = errors.New("episode is not cached")
	ErrInvalidInput = errors.New("invalid podcast cache input")
)

const defaultMaxFileBytes = 500 << 20

type Service struct {
	db           *sql.DB
	cacheDir     string
	enabled      bool
	maxBytes     int64
	maxAge       time.Duration
	maxFileBytes int64
	stream       *podcaststream.Service
	mu           sync.Mutex
	inflight     map[string]struct{}
}

type Options struct {
	CacheDir     string
	Enabled      bool
	MaxBytes     int64
	MaxAge       time.Duration
	MaxFileBytes int64
	Stream       *podcaststream.Service
}

func New(db *sql.DB, options Options) (*Service, error) {
	cacheDir := strings.TrimSpace(options.CacheDir)
	if cacheDir == "" {
		return nil, errors.New("podcast cache directory is required")
	}
	absolute, err := filepath.Abs(cacheDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return nil, fmt.Errorf("create podcast cache directory: %w", err)
	}
	stream := options.Stream
	if stream == nil {
		stream = podcaststream.New()
	}
	maxFileBytes := options.MaxFileBytes
	if maxFileBytes <= 0 {
		maxFileBytes = defaultMaxFileBytes
	}
	return &Service{
		db:           db,
		cacheDir:     absolute,
		enabled:      options.Enabled,
		maxBytes:     options.MaxBytes,
		maxAge:       options.MaxAge,
		maxFileBytes: maxFileBytes,
		stream:       stream,
		inflight:     map[string]struct{}{},
	}, nil
}

func (s *Service) Enabled() bool {
	return s != nil && s.enabled
}

func (s *Service) CacheDir() string {
	if s == nil {
		return ""
	}
	return s.cacheDir
}

type CachedFile struct {
	Path        string
	ContentType string
	SizeBytes   int64
}

func (s *Service) Lookup(ctx context.Context, episodeID, enclosureURL string) (CachedFile, bool, error) {
	if s == nil || s.db == nil || !s.enabled {
		return CachedFile{}, false, nil
	}
	episodeID = strings.TrimSpace(episodeID)
	enclosureURL = strings.TrimSpace(enclosureURL)
	if episodeID == "" || enclosureURL == "" {
		return CachedFile{}, false, ErrInvalidInput
	}
	row, found, err := loadCacheRow(ctx, s.db, episodeID)
	if err != nil || !found {
		return CachedFile{}, false, err
	}
	if strings.TrimSpace(row.EnclosureURL) != enclosureURL {
		return CachedFile{}, false, nil
	}
	info, err := os.Stat(row.CachePath)
	if err != nil || info.Size() == 0 {
		_ = s.removeCacheRow(ctx, episodeID, row.CachePath)
		return CachedFile{}, false, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `
		UPDATE podcast_episode_cache
		SET last_accessed_at = ?
		WHERE episode_id = ?`, now, episodeID); err != nil {
		return CachedFile{}, false, fmt.Errorf("touch podcast cache row: %w", err)
	}
	return CachedFile{
		Path:        row.CachePath,
		ContentType: row.ContentType,
		SizeBytes:   info.Size(),
	}, true, nil
}

func (s *Service) EnsureCached(ctx context.Context, episode catalog.PodcastEpisode) error {
	if s == nil || s.db == nil || !s.enabled {
		return ErrDisabled
	}
	episodeID := strings.TrimSpace(episode.ID)
	enclosureURL := strings.TrimSpace(episode.EnclosureURL)
	if episodeID == "" || enclosureURL == "" {
		return ErrInvalidInput
	}
	if cached, ok, err := s.Lookup(ctx, episodeID, enclosureURL); err != nil {
		return err
	} else if ok && cached.SizeBytes > 0 {
		return nil
	}

	s.mu.Lock()
	if _, busy := s.inflight[episodeID]; busy {
		s.mu.Unlock()
		return nil
	}
	s.inflight[episodeID] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, episodeID)
		s.mu.Unlock()
	}()

	return s.download(ctx, episodeID, enclosureURL, episode.EnclosureType)
}

func (s *Service) download(ctx context.Context, episodeID, enclosureURL, fallbackType string) error {
	if err := s.PruneRetention(ctx); err != nil {
		return err
	}

	body, contentType, err := s.stream.FetchEnclosure(ctx, enclosureURL, s.maxFileBytes)
	if err != nil {
		return err
	}
	defer body.Close()

	ext := extensionForURL(enclosureURL, contentType)
	cachePath := filepath.Join(s.cacheDir, episodeID+ext)
	tmpPath := cachePath + ".part"
	if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open cache temp file: %w", err)
	}
	written, err := io.Copy(file, body)
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write cache file: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close cache temp file: %w", closeErr)
	}
	if written == 0 {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("cache file is empty")
	}
	if err := os.Rename(tmpPath, cachePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("finalize cache file: %w", err)
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = strings.TrimSpace(fallbackType)
	}
	if existing, found, err := loadCacheRow(ctx, s.db, episodeID); err != nil {
		return err
	} else if found && strings.TrimSpace(existing.CachePath) != "" && existing.CachePath != cachePath {
		removeFileIfPresent(existing.CachePath)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO podcast_episode_cache (
		  episode_id, enclosure_url, cache_path, content_type, size_bytes, downloaded_at, last_accessed_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(episode_id) DO UPDATE SET
		  enclosure_url = excluded.enclosure_url,
		  cache_path = excluded.cache_path,
		  content_type = excluded.content_type,
		  size_bytes = excluded.size_bytes,
		  downloaded_at = excluded.downloaded_at,
		  last_accessed_at = excluded.last_accessed_at`,
		episodeID, enclosureURL, cachePath, contentType, written, now, now); err != nil {
		_ = os.Remove(cachePath)
		return fmt.Errorf("record podcast cache row: %w", err)
	}
	return nil
}

func (s *Service) removeCacheRow(ctx context.Context, episodeID, cachePath string) error {
	if strings.TrimSpace(cachePath) != "" {
		_ = os.Remove(cachePath)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM podcast_episode_cache WHERE episode_id = ?`, episodeID)
	return err
}

func extensionForURL(rawURL, contentType string) string {
	if parsed, err := url.Parse(strings.TrimSpace(rawURL)); err == nil {
		if ext := strings.ToLower(path.Ext(parsed.Path)); ext != "" && len(ext) <= 8 {
			return ext
		}
	}
	switch {
	case strings.Contains(contentType, "mpeg"):
		return ".mp3"
	case strings.Contains(contentType, "mp4"), strings.Contains(contentType, "m4a"):
		return ".m4a"
	case strings.Contains(contentType, "ogg"):
		return ".ogg"
	case strings.Contains(contentType, "wav"):
		return ".wav"
	default:
		return ".audio"
	}
}
