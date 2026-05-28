package artistimages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/lastfm"
)

const (
	negativeCacheTTL   = 30 * 24 * time.Hour
	externalFetchLimit = 2
)

type CatalogPatcher interface {
	SetMusicArtistImages(artistID string, images []catalog.Image)
}

type Service struct {
	db      *sql.DB
	lastfm  *lastfm.Service
	covers  *covers.Service
	catalog CatalogPatcher
	logger  func(format string, args ...any)
	http    *http.Client
	bgCtx   context.Context

	mu       sync.Mutex
	inflight map[string]*resolveCall
	sem      chan struct{}

	backfillMu     sync.Mutex
	activeBackfill *backfillRunner
	lastBackfill   *BackfillJob
}

type ServiceOptions struct {
	DB         *sql.DB
	LastFM     *lastfm.Service
	Covers     *covers.Service
	Catalog    CatalogPatcher
	Logger     func(format string, args ...any)
	HTTPClient *http.Client
}

func NewService(options ServiceOptions) *Service {
	logger := options.Logger
	if logger == nil {
		logger = func(string, ...any) {}
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Service{
		db:       options.DB,
		lastfm:   options.LastFM,
		covers:   options.Covers,
		catalog:  options.Catalog,
		logger:   logger,
		http:     httpClient,
		inflight: map[string]*resolveCall{},
		sem:      make(chan struct{}, externalFetchLimit),
	}
}

type resolveCall struct {
	done   chan struct{}
	images []catalog.Image
	found  bool
}

func (s *Service) Enabled() bool {
	return s != nil && s.db != nil && s.covers != nil
}

func (s *Service) ResolveMusicArtistCover(ctx context.Context, artist catalog.MusicArtist) ([]catalog.Image, bool) {
	if images := catalog.NonEmptyImages(artist.Images); len(images) > 0 {
		if hasLocalArtistImage(images) {
			return images, true
		}
	}

	if s == nil || s.db == nil {
		return nil, false
	}

	if cached, ok, err := s.loadCachedCover(ctx, artist.ID); err == nil && ok {
		s.patchCatalog(artist.ID, cached)
		return cached, true
	} else if err == nil && !ok {
		return nil, false
	}

	if !s.Enabled() {
		return nil, false
	}

	return s.resolveExternal(ctx, artist)
}

func hasLocalArtistImage(images []catalog.Image) bool {
	for _, image := range images {
		if strings.TrimSpace(image.Path) != "" {
			return true
		}
		if strings.TrimSpace(image.ID) != "" && strings.HasPrefix(strings.TrimSpace(image.ID), "cover_") {
			return true
		}
	}
	return false
}

func (s *Service) loadCachedCover(ctx context.Context, artistID string) ([]catalog.Image, bool, error) {
	row, err := loadCacheRow(ctx, s.db, artistID)
	if errors.Is(err, errCacheMiss) {
		return nil, false, errCacheMiss
	}
	if err != nil {
		return nil, false, err
	}
	if row.CoverID == "" {
		if row.FetchedAt.IsZero() || time.Since(row.FetchedAt) < negativeCacheTTL {
			return nil, false, nil
		}
		return nil, false, errCacheMiss
	}
	if s.covers == nil {
		return nil, false, fmt.Errorf("covers service unavailable")
	}
	image, err := s.covers.Get(ctx, row.CoverID)
	if err != nil {
		if time.Since(row.FetchedAt) < negativeCacheTTL {
			return nil, false, errCacheMiss
		}
		return nil, false, errCacheMiss
	}
	return []catalog.Image{image}, true, nil
}

func (s *Service) resolveExternal(ctx context.Context, artist catalog.MusicArtist) ([]catalog.Image, bool) {
	s.mu.Lock()
	if call, ok := s.inflight[artist.ID]; ok {
		s.mu.Unlock()
		<-call.done
		return call.images, call.found
	}
	call := &resolveCall{done: make(chan struct{})}
	s.inflight[artist.ID] = call
	s.mu.Unlock()

	defer func() {
		close(call.done)
		s.mu.Lock()
		delete(s.inflight, artist.ID)
		s.mu.Unlock()
	}()

	images, found := s.fetchAndPersist(ctx, artist)
	call.images = images
	call.found = found
	return images, found
}

func (s *Service) fetchAndPersist(ctx context.Context, artist catalog.MusicArtist) ([]catalog.Image, bool) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, false
	}

	imageURL, source, err := s.lookupArtistPictureURL(ctx, artist)
	if err != nil || strings.TrimSpace(imageURL) == "" {
		if err != nil {
			s.logger("artist image lookup failed for %q: %v", artist.Name, err)
		}
		_ = saveCacheRow(ctx, s.db, artist.ID, "", source)
		return nil, false
	}

	downloaded, err := s.covers.DownloadFromURL(ctx, imageURL)
	if err != nil || downloaded == nil {
		s.logger("artist image download failed for %q: %v", artist.Name, err)
		_ = saveCacheRow(ctx, s.db, artist.ID, "", source)
		return nil, false
	}

	images := []catalog.Image{*downloaded}
	if err := s.persistArtistImages(ctx, artist.ID, images, source); err != nil {
		s.logger("artist image persist failed for %q: %v", artist.Name, err)
		return images, true
	}
	s.patchCatalog(artist.ID, images)
	return images, true
}

func (s *Service) lookupArtistPictureURL(ctx context.Context, artist catalog.MusicArtist) (string, string, error) {
	if s.lastfm != nil {
		if client, ok := s.lastfm.ActiveClient(); ok && client.APIKeyConfigured() {
			mbid := strings.TrimSpace(artist.ExternalIDs.MusicBrainzArtistID)
			info, err := client.GetArtistInfo(ctx, artist.Name, mbid)
			if err == nil && strings.TrimSpace(info.Image) != "" {
				return info.Image, "lastfm", nil
			}
			if err != nil && mbid != "" {
				info, err = client.GetArtistInfo(ctx, artist.Name, "")
				if err == nil && strings.TrimSpace(info.Image) != "" {
					return info.Image, "lastfm", nil
				}
			}
		}
	}

	names := lookupArtistNames(artist)
	picture, err := deezerArtistPictureURL(ctx, s.http, names...)
	if err != nil {
		return "", "deezer", err
	}
	return picture, "deezer", nil
}

func lookupArtistNames(artist catalog.MusicArtist) []string {
	names := []string{
		strings.TrimSpace(artist.Name),
		strings.TrimSpace(artist.SortName),
	}
	if idx := strings.Index(artist.Name, ","); idx > 0 {
		names = append(names, strings.TrimSpace(artist.Name[:idx]))
	}
	if idx := strings.Index(artist.Name, " & "); idx > 0 {
		names = append(names, strings.TrimSpace(artist.Name[:idx]))
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

func (s *Service) persistArtistImages(ctx context.Context, artistID string, images []catalog.Image, source string) error {
	coverID := ""
	if len(images) > 0 {
		coverID = strings.TrimSpace(images[0].ID)
	}
	if err := saveCacheRow(ctx, s.db, artistID, coverID, source); err != nil {
		return err
	}
	payload, err := json.Marshal(images)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE music_artists
		SET images_json = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, string(payload), artistID)
	return err
}

func (s *Service) patchCatalog(artistID string, images []catalog.Image) {
	if s.catalog == nil || len(images) == 0 {
		return
	}
	s.catalog.SetMusicArtistImages(artistID, images)
}
