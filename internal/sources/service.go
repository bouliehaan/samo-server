package sources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jakedebus/samo-server/internal/catalog"
	"github.com/jakedebus/samo-server/internal/media"
)

const (
	remotePodcastLibraryID   = "library_remote_podcasts"
	remotePodcastLibraryPath = "samo://podcast-feeds"
	defaultSourceStatus      = "ok"
	maxFeedBytes             = 25 << 20
)

var (
	ErrNotFound   = errors.New("source not found")
	ErrInvalidURL = errors.New("invalid source url")
	ErrDisabled   = errors.New("source service is not configured")
)

type Service struct {
	db     *sql.DB
	client *http.Client
}

func New(db *sql.DB) *Service {
	return NewWithHTTPClient(db, &http.Client{Timeout: 30 * time.Second})
}

func NewWithHTTPClient(db *sql.DB, client *http.Client) *Service {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Service{db: db, client: client}
}

func (s *Service) AddPodcastFeed(ctx context.Context, input AddPodcastFeedInput) (PodcastFeed, error) {
	if s == nil || s.db == nil {
		return PodcastFeed{}, ErrDisabled
	}
	feedURL, err := normalizeHTTPURL(input.URL)
	if err != nil {
		return PodcastFeed{}, err
	}

	parsed, err := s.fetchPodcastFeed(ctx, feedURL)
	if err != nil {
		return PodcastFeed{}, err
	}
	if title := strings.TrimSpace(input.Title); title != "" {
		parsed.Title = title
	}
	if parsed.Title == "" {
		parsed.Title = feedURL
	}

	if err := s.savePodcastFeed(ctx, feedURL, parsed); err != nil {
		return PodcastFeed{}, err
	}
	return s.GetPodcastFeed(ctx, podcastFeedID(feedURL))
}

func (s *Service) RefreshPodcastFeed(ctx context.Context, id string) (PodcastFeed, error) {
	if s == nil || s.db == nil {
		return PodcastFeed{}, ErrDisabled
	}
	existing, err := s.GetPodcastFeed(ctx, id)
	if err != nil {
		return PodcastFeed{}, err
	}

	parsed, err := s.fetchPodcastFeed(ctx, existing.FeedURL)
	if err != nil {
		_ = s.recordPodcastFeedError(ctx, existing.ID, err)
		return PodcastFeed{}, err
	}
	if parsed.Title == "" {
		parsed.Title = existing.Title
	}
	if err := s.savePodcastFeed(ctx, existing.FeedURL, parsed); err != nil {
		return PodcastFeed{}, err
	}
	return s.GetPodcastFeed(ctx, existing.ID)
}

func (s *Service) ListPodcastFeeds(ctx context.Context, page catalog.PageRequest) (catalog.Page[PodcastFeed], error) {
	if s == nil || s.db == nil {
		return catalog.Page[PodcastFeed]{}, ErrDisabled
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, podcast_id, feed_url, title, description, author, site_url, image_url, language,
		       explicit, categories_json, owner_name, owner_email, episode_count, status, last_error,
		       last_fetched_at, created_at, updated_at
		FROM podcast_feeds
		ORDER BY title COLLATE NOCASE`)
	if err != nil {
		return catalog.Page[PodcastFeed]{}, fmt.Errorf("list podcast feeds: %w", err)
	}
	defer rows.Close()

	var feeds []PodcastFeed
	for rows.Next() {
		feed, err := scanPodcastFeed(rows)
		if err != nil {
			return catalog.Page[PodcastFeed]{}, err
		}
		feeds = append(feeds, feed)
	}
	if err := rows.Err(); err != nil {
		return catalog.Page[PodcastFeed]{}, err
	}
	return paginate(feeds, page), nil
}

func (s *Service) GetPodcastFeed(ctx context.Context, id string) (PodcastFeed, error) {
	if s == nil || s.db == nil {
		return PodcastFeed{}, ErrDisabled
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, podcast_id, feed_url, title, description, author, site_url, image_url, language,
		       explicit, categories_json, owner_name, owner_email, episode_count, status, last_error,
		       last_fetched_at, created_at, updated_at
		FROM podcast_feeds
		WHERE id = ?`, strings.TrimSpace(id))
	feed, err := scanPodcastFeed(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PodcastFeed{}, ErrNotFound
	}
	if err != nil {
		return PodcastFeed{}, err
	}
	return feed, nil
}

func (s *Service) DeletePodcastFeed(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var podcastID string
	if err := tx.QueryRowContext(ctx, `SELECT podcast_id FROM podcast_feeds WHERE id = ?`, strings.TrimSpace(id)).Scan(&podcastID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("find podcast feed: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM shelf_items WHERE id = ?`, podcastID); err != nil {
		return fmt.Errorf("delete podcast feed item: %w", err)
	}
	if err := refreshRemotePodcastLibraryStats(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) AddInternetRadioStation(ctx context.Context, input AddInternetRadioStationInput) (InternetRadioStation, error) {
	if s == nil || s.db == nil {
		return InternetRadioStation{}, ErrDisabled
	}
	streamURL, err := normalizeHTTPURL(input.StreamURL)
	if err != nil {
		return InternetRadioStation{}, err
	}
	homepageURL, err := normalizeOptionalHTTPURL(input.HomepageURL)
	if err != nil {
		return InternetRadioStation{}, err
	}
	imageURL, err := normalizeOptionalHTTPURL(input.ImageURL)
	if err != nil {
		return InternetRadioStation{}, err
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = defaultStationName(streamURL)
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	id := internetRadioStationID(streamURL)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO internet_radio_stations (
		  id, name, description, stream_url, homepage_url, image_url, content_type, codec, bitrate,
		  country, language, tags_json, enabled, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  description = excluded.description,
		  stream_url = excluded.stream_url,
		  homepage_url = excluded.homepage_url,
		  image_url = excluded.image_url,
		  content_type = excluded.content_type,
		  codec = excluded.codec,
		  bitrate = excluded.bitrate,
		  country = excluded.country,
		  language = excluded.language,
		  tags_json = excluded.tags_json,
		  enabled = excluded.enabled,
		  updated_at = CURRENT_TIMESTAMP`,
		id, name, strings.TrimSpace(input.Description), streamURL, homepageURL, imageURL,
		strings.TrimSpace(input.ContentType), strings.TrimSpace(input.Codec), input.Bitrate,
		strings.TrimSpace(input.Country), strings.TrimSpace(input.Language), jsonText(cleanStringSlice(input.Tags)), boolInt(enabled))
	if err != nil {
		return InternetRadioStation{}, fmt.Errorf("upsert internet radio station: %w", err)
	}
	return s.GetInternetRadioStation(ctx, id)
}

func (s *Service) ListInternetRadioStations(ctx context.Context, page catalog.PageRequest) (catalog.Page[InternetRadioStation], error) {
	if s == nil || s.db == nil {
		return catalog.Page[InternetRadioStation]{}, ErrDisabled
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, stream_url, homepage_url, image_url, content_type, codec, bitrate,
		       country, language, tags_json, enabled, last_checked_at, created_at, updated_at
		FROM internet_radio_stations
		ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return catalog.Page[InternetRadioStation]{}, fmt.Errorf("list internet radio stations: %w", err)
	}
	defer rows.Close()

	var stations []InternetRadioStation
	for rows.Next() {
		station, err := scanInternetRadioStation(rows)
		if err != nil {
			return catalog.Page[InternetRadioStation]{}, err
		}
		stations = append(stations, station)
	}
	if err := rows.Err(); err != nil {
		return catalog.Page[InternetRadioStation]{}, err
	}
	return paginate(stations, page), nil
}

func (s *Service) GetInternetRadioStation(ctx context.Context, id string) (InternetRadioStation, error) {
	if s == nil || s.db == nil {
		return InternetRadioStation{}, ErrDisabled
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, stream_url, homepage_url, image_url, content_type, codec, bitrate,
		       country, language, tags_json, enabled, last_checked_at, created_at, updated_at
		FROM internet_radio_stations
		WHERE id = ?`, strings.TrimSpace(id))
	station, err := scanInternetRadioStation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return InternetRadioStation{}, ErrNotFound
	}
	if err != nil {
		return InternetRadioStation{}, err
	}
	return station, nil
}

func (s *Service) DeleteInternetRadioStation(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM internet_radio_stations WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("delete internet radio station: %w", err)
	}
	rows, err := result.RowsAffected()
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Service) fetchPodcastFeed(ctx context.Context, feedURL string) (parsedPodcastFeed, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return parsedPodcastFeed{}, err
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.5")
	req.Header.Set("User-Agent", "Samo Server/0.1")

	resp, err := s.client.Do(req)
	if err != nil {
		return parsedPodcastFeed{}, fmt.Errorf("fetch podcast feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parsedPodcastFeed{}, fmt.Errorf("fetch podcast feed: status %d", resp.StatusCode)
	}
	return parsePodcastFeedXML(io.LimitReader(resp.Body, maxFeedBytes))
}

func (s *Service) savePodcastFeed(ctx context.Context, feedURL string, parsed parsedPodcastFeed) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := upsertRemotePodcastLibrary(ctx, tx); err != nil {
		return err
	}

	feedID := podcastFeedID(feedURL)
	podcastID := podcastItemID(feedURL)
	episodeCount := len(parsed.Episodes)
	categories := cleanStringSlice(parsed.Categories)
	podcastMeta := catalog.PodcastMetadata{
		Title:        parsed.Title,
		Author:       parsed.Author,
		Description:  parsed.Description,
		FeedURL:      feedURL,
		SiteURL:      parsed.SiteURL,
		Language:     parsed.Language,
		Explicit:     parsed.Explicit,
		Categories:   categories,
		OwnerName:    parsed.OwnerName,
		OwnerEmail:   parsed.OwnerEmail,
		EpisodeCount: episodeCount,
		ExternalIDs: catalog.ExternalIDs{
			FeedGUID: feedID,
			URLs:     cleanStringSlice([]string{feedURL, parsed.SiteURL}),
		},
	}
	coverJSON := "{}"
	if parsed.ImageURL != "" {
		coverJSON = jsonText(catalog.Image{URL: parsed.ImageURL})
	}

	durationSeconds := 0
	for _, episode := range parsed.Episodes {
		durationSeconds += episode.DurationSeconds
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO shelf_items (
		  id, library_id, media_type, media_kind, path, folder_id, cover_json, tags_json, genres_json,
		  duration_seconds, podcast_json, updated_at, last_scan_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  library_id = excluded.library_id,
		  media_type = excluded.media_type,
		  media_kind = excluded.media_kind,
		  path = excluded.path,
		  folder_id = excluded.folder_id,
		  cover_json = excluded.cover_json,
		  tags_json = excluded.tags_json,
		  genres_json = excluded.genres_json,
		  duration_seconds = excluded.duration_seconds,
		  podcast_json = excluded.podcast_json,
		  updated_at = CURRENT_TIMESTAMP,
		  last_scan_at = CURRENT_TIMESTAMP`,
		podcastID, remotePodcastLibraryID, catalog.ShelfMediaTypePodcast, media.KindPodcast, feedURL,
		stableID("folder", feedURL), coverJSON, jsonText(categories), jsonText(categories), durationSeconds, jsonText(podcastMeta)); err != nil {
		return fmt.Errorf("upsert podcast feed item: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO podcast_feeds (
		  id, podcast_id, feed_url, title, description, author, site_url, image_url, language, explicit,
		  categories_json, owner_name, owner_email, episode_count, status, last_error, last_fetched_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  podcast_id = excluded.podcast_id,
		  feed_url = excluded.feed_url,
		  title = excluded.title,
		  description = excluded.description,
		  author = excluded.author,
		  site_url = excluded.site_url,
		  image_url = excluded.image_url,
		  language = excluded.language,
		  explicit = excluded.explicit,
		  categories_json = excluded.categories_json,
		  owner_name = excluded.owner_name,
		  owner_email = excluded.owner_email,
		  episode_count = excluded.episode_count,
		  status = excluded.status,
		  last_error = excluded.last_error,
		  last_fetched_at = CURRENT_TIMESTAMP,
		  updated_at = CURRENT_TIMESTAMP`,
		feedID, podcastID, feedURL, parsed.Title, parsed.Description, parsed.Author, parsed.SiteURL, parsed.ImageURL,
		parsed.Language, boolInt(parsed.Explicit), jsonText(categories), parsed.OwnerName, parsed.OwnerEmail,
		episodeCount, defaultSourceStatus); err != nil {
		return fmt.Errorf("upsert podcast feed source: %w", err)
	}

	for _, parsedEpisode := range parsed.Episodes {
		episodeID := podcastEpisodeID(podcastID, parsedEpisode)
		externalIDs := catalog.ExternalIDs{
			FeedGUID: parsedEpisode.GUID,
			URLs:     cleanStringSlice([]string{parsedEpisode.Link, parsedEpisode.EnclosureURL}),
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO podcast_episodes (
			  id, library_id, podcast_id, title, subtitle, description, published_at, season, episode,
			  episode_type, duration_seconds, explicit, enclosure_url, enclosure_type, enclosure_bytes,
			  external_ids_json, updated_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(id) DO UPDATE SET
			  library_id = excluded.library_id,
			  podcast_id = excluded.podcast_id,
			  title = excluded.title,
			  subtitle = excluded.subtitle,
			  description = excluded.description,
			  published_at = excluded.published_at,
			  season = excluded.season,
			  episode = excluded.episode,
			  episode_type = excluded.episode_type,
			  duration_seconds = excluded.duration_seconds,
			  explicit = excluded.explicit,
			  enclosure_url = excluded.enclosure_url,
			  enclosure_type = excluded.enclosure_type,
			  enclosure_bytes = excluded.enclosure_bytes,
			  external_ids_json = excluded.external_ids_json,
			  updated_at = CURRENT_TIMESTAMP`,
			episodeID, remotePodcastLibraryID, podcastID, parsedEpisode.Title, parsedEpisode.Subtitle,
			parsedEpisode.Description, timeString(parsedEpisode.PublishedAt), parsedEpisode.Season,
			parsedEpisode.Episode, parsedEpisode.EpisodeType, parsedEpisode.DurationSeconds,
			boolInt(parsedEpisode.Explicit), parsedEpisode.EnclosureURL, parsedEpisode.EnclosureType,
			parsedEpisode.EnclosureBytes, jsonText(externalIDs)); err != nil {
			return fmt.Errorf("upsert feed episode %q: %w", parsedEpisode.Title, err)
		}
	}

	if err := refreshRemotePodcastLibraryStats(ctx, tx); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Service) recordPodcastFeedError(ctx context.Context, id string, cause error) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE podcast_feeds
		SET status = 'error', last_error = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, strings.TrimSpace(cause.Error()), strings.TrimSpace(id))
	return err
}

func upsertRemotePodcastLibrary(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, description, updated_at)
		VALUES (?, 'Podcast Feeds', 'shelf', ?, ?, 'Remote RSS podcast subscriptions.', CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  kind = excluded.kind,
		  media_type = excluded.media_type,
		  path = excluded.path,
		  description = excluded.description,
		  updated_at = CURRENT_TIMESTAMP`,
		remotePodcastLibraryID, catalog.ShelfMediaTypePodcast, remotePodcastLibraryPath)
	if err != nil {
		return fmt.Errorf("upsert remote podcast library: %w", err)
	}
	return nil
}

func refreshRemotePodcastLibraryStats(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE libraries
		SET item_count = COALESCE((SELECT COUNT(*) FROM shelf_items WHERE library_id = libraries.id), 0),
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, remotePodcastLibraryID)
	if err != nil {
		return fmt.Errorf("refresh remote podcast library stats: %w", err)
	}
	return nil
}

func podcastFeedID(feedURL string) string {
	return stableID("feed", feedURL)
}

func podcastItemID(feedURL string) string {
	return stableID("podcast", "rss", feedURL)
}

func podcastEpisodeID(podcastID string, episode parsedPodcastEpisode) string {
	key := firstNonEmpty(episode.GUID, episode.EnclosureURL, episode.Link, episode.Title)
	return stableID("episode", podcastID, key)
}

func internetRadioStationID(streamURL string) string {
	return stableID("internet-radio", streamURL)
}

func stableID(prefix string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write([]byte(strings.ToLower(strings.TrimSpace(part))))
		hash.Write([]byte{0})
	}
	sum := hash.Sum(nil)
	return prefix + "_" + hex.EncodeToString(sum[:12])
}

func normalizeHTTPURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", ErrInvalidURL
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", ErrInvalidURL
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	return parsed.String(), nil
}

func normalizeOptionalHTTPURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	return normalizeHTTPURL(raw)
}

func defaultStationName(streamURL string) string {
	parsed, err := url.Parse(streamURL)
	if err != nil {
		return "Internet Radio"
	}
	name := strings.Trim(strings.TrimSuffix(path.Base(parsed.Path), path.Ext(parsed.Path)), "/")
	if name == "" || name == "." {
		name = parsed.Host
	}
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	return strings.TrimSpace(name)
}

func cleanStringSlice(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	sort.SliceStable(cleaned, func(i, j int) bool {
		return strings.ToLower(cleaned[i]) < strings.ToLower(cleaned[j])
	})
	return uniqueStrings(cleaned)
}

func jsonText(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func timeString(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}
