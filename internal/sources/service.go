package sources

import (
	"bytes"
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

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/podcastcache"
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
	db                  *sql.DB
	client              *http.Client
	covers              *covers.Service
	podcastCache        *podcastcache.Service
	defaultAutoDownload bool
}

type Options struct {
	HTTPClient          *http.Client
	Covers              *covers.Service
	PodcastCache        *podcastcache.Service
	DefaultAutoDownload bool
}

func New(db *sql.DB, opts ...Options) *Service {
	var options Options
	if len(opts) > 0 {
		options = opts[0]
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Service{
		db:                  db,
		client:              client,
		covers:              options.Covers,
		podcastCache:        options.PodcastCache,
		defaultAutoDownload: options.DefaultAutoDownload,
	}
}

func NewWithHTTPClient(db *sql.DB, client *http.Client) *Service {
	return New(db, Options{HTTPClient: client})
}

func (s *Service) AddPodcastFeed(ctx context.Context, input AddPodcastFeedInput) (PodcastFeed, error) {
	if s == nil || s.db == nil {
		return PodcastFeed{}, ErrDisabled
	}
	feedURL, err := normalizeHTTPURL(input.URL)
	if err != nil {
		return PodcastFeed{}, err
	}

	parsed, resolvedFeedURL, err := s.fetchPodcastFeed(ctx, feedURL)
	if err != nil {
		return PodcastFeed{}, err
	}
	if title := strings.TrimSpace(input.Title); title != "" {
		parsed.Title = title
	}
	if parsed.Title == "" {
		parsed.Title = resolvedFeedURL
	}

	attachPodcastID := strings.TrimSpace(input.PodcastID)
	if attachPodcastID != "" {
		if _, err := s.loadPodcastShowRow(ctx, attachPodcastID); err != nil {
			return PodcastFeed{}, err
		}
		hasFeed, err := s.podcastHasFeed(ctx, attachPodcastID)
		if err != nil {
			return PodcastFeed{}, err
		}
		if hasFeed {
			return PodcastFeed{}, ErrPodcastAlreadyHasFeed
		}
		if existingPodcastID, ok, err := s.feedURLPodcastID(ctx, resolvedFeedURL); err != nil {
			return PodcastFeed{}, err
		} else if ok && existingPodcastID != attachPodcastID {
			return PodcastFeed{}, ErrFeedURLInUse
		}
	} else if existingPodcastID, ok, err := s.feedURLPodcastID(ctx, resolvedFeedURL); err != nil {
		return PodcastFeed{}, err
	} else if ok {
		if _, err := s.loadPodcastShowRow(ctx, existingPodcastID); err == nil {
			attachPodcastID = existingPodcastID
		}
	}

	if err := s.savePodcastFeed(ctx, resolvedFeedURL, parsed, feedSaveOptions{
		autoDownloadOnInsert: s.resolveAutoDownload(input.AutoDownloadEnabled),
		attachPodcastID:      attachPodcastID,
	}); err != nil {
		return PodcastFeed{}, err
	}
	return s.GetPodcastFeed(ctx, podcastFeedID(resolvedFeedURL))
}

// LinkOrRefreshPodcastFeedForShow attaches an RSS feed to a filesystem podcast
// show, or re-fetches the existing feed to merge episode metadata (e.g. dates).
func (s *Service) LinkOrRefreshPodcastFeedForShow(ctx context.Context, podcastID, feedURL string) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	podcastID = strings.TrimSpace(podcastID)
	feedURL = strings.TrimSpace(feedURL)
	if podcastID == "" || feedURL == "" {
		return nil
	}
	hasFeed, err := s.podcastHasFeed(ctx, podcastID)
	if err != nil {
		return err
	}
	if hasFeed {
		var feedID string
		if err := s.db.QueryRowContext(ctx, `
			SELECT id FROM podcast_feeds WHERE podcast_id = ? LIMIT 1`, podcastID).
			Scan(&feedID); err != nil {
			return fmt.Errorf("load podcast feed: %w", err)
		}
		_, err := s.RefreshPodcastFeed(ctx, feedID)
		return err
	}
	_, err = s.AddPodcastFeed(ctx, AddPodcastFeedInput{
		PodcastID: podcastID,
		URL:       feedURL,
	})
	return err
}

func (s *Service) PodcastFeedForShow(ctx context.Context, podcastID string) (PodcastFeed, error) {
	if s == nil || s.db == nil {
		return PodcastFeed{}, ErrDisabled
	}
	podcastID = strings.TrimSpace(podcastID)
	if podcastID == "" {
		return PodcastFeed{}, ErrNotFound
	}
	var feedID string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM podcast_feeds WHERE podcast_id = ? LIMIT 1`, podcastID).Scan(&feedID)
	if errors.Is(err, sql.ErrNoRows) {
		return PodcastFeed{}, ErrNotFound
	}
	if err != nil {
		return PodcastFeed{}, fmt.Errorf("find podcast feed: %w", err)
	}
	return s.GetPodcastFeed(ctx, feedID)
}

func (s *Service) RefreshPodcastFeed(ctx context.Context, id string) (PodcastFeed, error) {
	if s == nil || s.db == nil {
		return PodcastFeed{}, ErrDisabled
	}
	existing, err := s.GetPodcastFeed(ctx, id)
	if err != nil {
		return PodcastFeed{}, err
	}

	if err := s.markPollStarted(ctx, existing.ID); err != nil {
		return PodcastFeed{}, err
	}

	parsed, _, err := s.fetchPodcastFeed(ctx, existing.FeedURL)
	if err != nil {
		_ = s.markPollFailure(ctx, existing.ID, err)
		return PodcastFeed{}, err
	}
	if parsed.Title == "" {
		parsed.Title = existing.Title
	}
	if err := s.savePodcastFeed(ctx, existing.FeedURL, parsed); err != nil {
		_ = s.markPollFailure(ctx, existing.ID, err)
		return PodcastFeed{}, err
	}
	if err := s.markPollSuccess(ctx, existing.ID, existing.Poll.IntervalSeconds); err != nil {
		return PodcastFeed{}, err
	}
	return s.GetPodcastFeed(ctx, existing.ID)
}

func (s *Service) ListPodcastFeeds(ctx context.Context, page catalog.PageRequest) (catalog.Page[PodcastFeed], error) {
	if s == nil || s.db == nil {
		return catalog.Page[PodcastFeed]{}, ErrDisabled
	}
	rows, err := s.db.QueryContext(ctx, podcastFeedSelectSQL+`
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
	for index, feed := range feeds {
		projected, err := s.projectPodcastFeed(ctx, feed)
		if err != nil {
			return catalog.Page[PodcastFeed]{}, err
		}
		feeds[index] = projected
	}
	return paginate(feeds, page), nil
}

func (s *Service) GetPodcastFeed(ctx context.Context, id string) (PodcastFeed, error) {
	if s == nil || s.db == nil {
		return PodcastFeed{}, ErrDisabled
	}
	row := s.db.QueryRowContext(ctx, podcastFeedSelectSQL+`
		WHERE id = ?`, strings.TrimSpace(id))
	feed, err := scanPodcastFeed(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PodcastFeed{}, ErrNotFound
	}
	if err != nil {
		return PodcastFeed{}, err
	}
	return s.projectPodcastFeed(ctx, feed)
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
	isHybrid, err := s.isFilesystemPodcast(ctx, podcastID)
	if err != nil {
		return err
	}
	if isHybrid {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM podcast_episodes
			WHERE podcast_id = ?
			  AND id NOT IN (
			    SELECT DISTINCT episode_id FROM media_files
			    WHERE episode_id IS NOT NULL AND TRIM(episode_id) != ''
			  )`, podcastID); err != nil {
			return fmt.Errorf("delete hybrid rss episodes: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM podcast_feeds WHERE id = ?`, strings.TrimSpace(id)); err != nil {
			return fmt.Errorf("delete hybrid podcast feed: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM podcasts WHERE id = ?`, podcastID); err != nil {
			return fmt.Errorf("delete podcast feed item: %w", err)
		}
		if err := refreshRemotePodcastLibraryStats(ctx, tx); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM metadata_overrides
		WHERE target_kind = ? AND target_id = ?`, catalog.OverrideKindPodcastFeed, strings.TrimSpace(id)); err != nil {
		return fmt.Errorf("delete podcast feed override: %w", err)
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
		  country, language, tags_json, enabled, updated_at,
		  probe_enabled, probe_interval_seconds, next_probe_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, 1, ?, ?)
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
		strings.TrimSpace(input.Country), strings.TrimSpace(input.Language), jsonText(cleanStringSlice(input.Tags)), boolInt(enabled),
		DefaultProbeIntervalSeconds, scheduleInitialPoll())
	if err != nil {
		return InternetRadioStation{}, fmt.Errorf("upsert internet radio station: %w", err)
	}
	return s.GetInternetRadioStation(ctx, id)
}

func (s *Service) ListInternetRadioStations(ctx context.Context, page catalog.PageRequest) (catalog.Page[InternetRadioStation], error) {
	if s == nil || s.db == nil {
		return catalog.Page[InternetRadioStation]{}, ErrDisabled
	}
	rows, err := s.db.QueryContext(ctx, internetRadioStationSelectSQL+`
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
	row := s.db.QueryRowContext(ctx, internetRadioStationSelectSQL+`
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

func (s *Service) SetInternetRadioCover(ctx context.Context, id, coverID string) (InternetRadioStation, error) {
	if s == nil || s.db == nil {
		return InternetRadioStation{}, ErrDisabled
	}
	id = strings.TrimSpace(id)
	coverID = strings.TrimSpace(coverID)
	if id == "" {
		return InternetRadioStation{}, ErrNotFound
	}
	if _, err := s.GetInternetRadioStation(ctx, id); err != nil {
		return InternetRadioStation{}, err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE internet_radio_stations
		SET cover_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, coverID, id)
	if err != nil {
		return InternetRadioStation{}, fmt.Errorf("set internet radio cover: %w", err)
	}
	return s.GetInternetRadioStation(ctx, id)
}

func (s *Service) fetchPodcastFeed(ctx context.Context, feedURL string) (parsedPodcastFeed, string, error) {
	return s.fetchPodcastFeedURL(ctx, feedURL, true)
}

func (s *Service) fetchPodcastFeedURL(ctx context.Context, feedURL string, allowDiscovery bool) (parsedPodcastFeed, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return parsedPodcastFeed{}, "", err
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.5")
	req.Header.Set("User-Agent", "Samo Server/0.1")

	resp, err := s.client.Do(req)
	if err != nil {
		return parsedPodcastFeed{}, "", fmt.Errorf("fetch podcast feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parsedPodcastFeed{}, "", fmt.Errorf("fetch podcast feed: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return parsedPodcastFeed{}, "", fmt.Errorf("read podcast feed: %w", err)
	}
	if len(body) > maxFeedBytes {
		return parsedPodcastFeed{}, "", fmt.Errorf("podcast feed exceeds %d bytes", maxFeedBytes)
	}

	resolvedURL := feedURL
	if resp.Request != nil && resp.Request.URL != nil {
		resolvedURL = resp.Request.URL.String()
	}
	parsed, parseErr := parsePodcastFeedXML(bytes.NewReader(body))
	if parseErr == nil {
		return parsed, resolvedURL, nil
	}
	if allowDiscovery && resp.Request != nil {
		if discovered, ok := discoverPodcastRSSURL(body, resp.Request.URL); ok && discovered != "" && discovered != resolvedURL {
			return s.fetchPodcastFeedURL(ctx, discovered, false)
		}
	}
	return parsedPodcastFeed{}, "", parseErr
}

func (s *Service) resolveAutoDownload(input *bool) bool {
	if input != nil {
		return *input
	}
	if s == nil {
		return false
	}
	return s.defaultAutoDownload
}

type feedSaveOptions struct {
	autoDownloadOnInsert bool
	attachPodcastID      string
}

func (s *Service) savePodcastFeed(ctx context.Context, feedURL string, parsed parsedPodcastFeed, opts ...feedSaveOptions) error {
	idx, err := catalog.LoadOverrideIndex(ctx, s.db)
	if err != nil {
		return err
	}

	feedID := podcastFeedID(feedURL)
	podcastID := podcastItemID(feedURL)
	hybrid := false
	var hybridShow podcastShowRow

	var saveOpts feedSaveOptions
	if len(opts) > 0 {
		saveOpts = opts[0]
	}
	if attachID := strings.TrimSpace(saveOpts.attachPodcastID); attachID != "" {
		show, err := s.loadPodcastShowRow(ctx, attachID)
		if err != nil {
			return err
		}
		hybridShow = show
		podcastID = show.ID
		hybrid = true
	} else if existingPodcastID, ok, err := s.feedURLPodcastID(ctx, feedURL); err != nil {
		return err
	} else if ok {
		podcastID = existingPodcastID
		if show, err := s.loadPodcastShowRow(ctx, podcastID); err == nil {
			hybridShow = show
			hybrid = true
		}
	}

	parsed, err = s.guardPodcastFeedSave(ctx, idx, feedID, podcastID, parsed)
	if err != nil {
		return err
	}

	episodeLibraryID := remotePodcastLibraryID
	var guardedEpisodes []catalog.PodcastEpisode
	if hybrid {
		episodeLibraryID = hybridShow.LibraryID
		existing, err := s.loadExistingEpisodesForMatch(ctx, podcastID)
		if err != nil {
			return err
		}
		plans := buildHybridEpisodePlans(podcastID, episodeLibraryID, parsed.Episodes, existing)
		guardedEpisodes = make([]catalog.PodcastEpisode, 0, len(plans))
		for _, episode := range plans {
			guarded, err := s.guardPodcastEpisodeSave(ctx, idx, episode)
			if err != nil {
				return err
			}
			guardedEpisodes = append(guardedEpisodes, guarded)
		}
	} else {
		var err error
		guardedEpisodes, err = s.guardPodcastEpisodesSave(ctx, idx, podcastID, episodeLibraryID, parsed.Episodes)
		if err != nil {
			return err
		}
	}

	autoDownload := false
	autoDownloadInsert := boolInt(saveOpts.autoDownloadOnInsert)
	var existingAutoDownload sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT auto_download_enabled FROM podcast_feeds WHERE id = ?`, feedID).
		Scan(&existingAutoDownload); err == sql.ErrNoRows {
		autoDownload = saveOpts.autoDownloadOnInsert
	} else if err != nil {
		return fmt.Errorf("load podcast feed auto download: %w", err)
	} else {
		autoDownload = existingAutoDownload.Int64 != 0
	}

	existingEpisodeIDs, err := s.loadPodcastEpisodeIDs(ctx, podcastID)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if !hybrid {
		if err := upsertRemotePodcastLibrary(ctx, tx); err != nil {
			return err
		}
	}

	episodeCount := len(guardedEpisodes)
	categories := cleanStringSlice(parsed.Categories)
	coverJSON, err := s.resolvePodcastFeedCoverJSON(ctx, idx, podcastID, parsed.ImageURL)
	if err != nil {
		return err
	}

	durationSeconds := 0
	for _, episode := range guardedEpisodes {
		durationSeconds += episode.DurationSeconds
	}

	if hybrid {
		podcastMeta := mergePodcastMetadataForHybrid(hybridShow.Podcast, feedURL, parsed)
		podcastMeta.EpisodeCount = episodeCount
		if _, err := tx.ExecContext(ctx, `
			UPDATE podcasts
			SET cover_json = CASE WHEN TRIM(?) != '' THEN ? ELSE cover_json END,
			    genres_json = ?,
			    duration_seconds = ?,
			    podcast_json = ?,
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			coverJSON, coverJSON,
			jsonText(categories),
			durationSeconds, jsonText(podcastMeta), podcastID); err != nil {
			return fmt.Errorf("update hybrid podcast: %w", err)
		}
	} else {
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
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO podcasts (
			  id, library_id, path, folder_id, cover_json, tags_json, genres_json,
			  duration_seconds, podcast_json, updated_at, last_scan_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			ON CONFLICT(id) DO UPDATE SET
			  library_id = excluded.library_id,
			  path = excluded.path,
			  folder_id = excluded.folder_id,
			  cover_json = excluded.cover_json,
			  tags_json = excluded.tags_json,
			  genres_json = excluded.genres_json,
			  duration_seconds = excluded.duration_seconds,
			  podcast_json = excluded.podcast_json,
			  updated_at = CURRENT_TIMESTAMP,
			  last_scan_at = CURRENT_TIMESTAMP`,
			podcastID, remotePodcastLibraryID, feedURL,
			stableID("folder", feedURL), coverJSON, jsonText(categories), jsonText(categories), durationSeconds, jsonText(podcastMeta)); err != nil {
			return fmt.Errorf("upsert podcast feed item: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO podcast_feeds (
		  id, podcast_id, feed_url, title, description, author, site_url, image_url, language, explicit,
		  categories_json, owner_name, owner_email, episode_count, status, last_error, last_fetched_at,
		  auto_download_enabled, poll_enabled, poll_interval_seconds, next_poll_at, consecutive_errors, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', CURRENT_TIMESTAMP, ?, 1, ?, ?, 0, CURRENT_TIMESTAMP)
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
		episodeCount, defaultSourceStatus, autoDownloadInsert, DefaultPollIntervalSeconds, scheduleInitialPoll()); err != nil {
		return fmt.Errorf("upsert podcast feed source: %w", err)
	}

	for _, episode := range guardedEpisodes {
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
			episode.ID, episode.LibraryID, episode.PodcastID, episode.Title, episode.Subtitle,
			episode.Description, timeString(episode.PublishedAt), episode.Season,
			episode.Episode, episode.EpisodeType, episode.DurationSeconds,
			boolInt(episode.Explicit), episode.EnclosureURL, episode.EnclosureType,
			episode.EnclosureBytes, jsonText(episode.ExternalIDs)); err != nil {
			return fmt.Errorf("upsert feed episode %q: %w", episode.Title, err)
		}
	}

	if !hybrid {
		if err := refreshRemotePodcastLibraryStats(ctx, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	var newEpisodes []catalog.PodcastEpisode
	for _, episode := range guardedEpisodes {
		if _, seen := existingEpisodeIDs[episode.ID]; seen {
			continue
		}
		newEpisodes = append(newEpisodes, episode)
	}
	if autoDownload && len(newEpisodes) > 0 {
		s.prefetchPodcastEpisodes(newEpisodes)
	}

	if s.podcastCache != nil {
		return s.podcastCache.PruneAfterFeedSave(ctx)
	}
	return nil
}

func (s *Service) recordPodcastFeedError(ctx context.Context, id string, cause error) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE podcast_feeds
		SET status = 'error', last_error = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, strings.TrimSpace(cause.Error()), strings.TrimSpace(id))
	return err
}

func upsertRemotePodcastLibrary(ctx context.Context, tx *sql.Tx) error {
	// media_type is intentionally NULL on `podcast` libraries — it is a
	// leftover column from the old shelf-era schema.
	_, err := tx.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, description, updated_at)
		VALUES (?, 'Podcast Feeds', 'podcast', NULL, ?, 'Remote RSS podcast subscriptions.', CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  kind = excluded.kind,
		  media_type = excluded.media_type,
		  path = excluded.path,
		  description = excluded.description,
		  updated_at = CURRENT_TIMESTAMP`,
		remotePodcastLibraryID, remotePodcastLibraryPath)
	if err != nil {
		return fmt.Errorf("upsert remote podcast library: %w", err)
	}
	return nil
}

func refreshRemotePodcastLibraryStats(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE libraries
		SET item_count = COALESCE((SELECT COUNT(*) FROM podcasts WHERE library_id = libraries.id), 0),
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

func (s *Service) resolvePodcastFeedCoverJSON(
	ctx context.Context,
	idx *catalog.OverrideIndex,
	podcastID, rssImageURL string,
) (string, error) {
	podcastID = strings.TrimSpace(podcastID)
	if podcastID == "" {
		return "{}", nil
	}
	if idx != nil {
		if patch := idx.Patch(catalog.OverrideKindPodcast, podcastID); len(patch) > 0 {
			if _, hasCover := patch["cover"]; hasCover {
				var existing string
				if err := s.db.QueryRowContext(ctx, `SELECT cover_json FROM podcasts WHERE id = ?`, podcastID).Scan(&existing); err == nil {
					existing = strings.TrimSpace(existing)
					if existing != "" && existing != "{}" && existing != "null" {
						return existing, nil
					}
				}
			}
		}
	}
	rssImageURL = strings.TrimSpace(rssImageURL)
	if rssImageURL == "" {
		return "{}", nil
	}
	if s.covers != nil {
		if image, err := s.covers.DownloadFromURL(ctx, rssImageURL); err == nil && image != nil {
			return jsonText(*image), nil
		}
	}
	return jsonText(catalog.Image{URL: rssImageURL}), nil
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
