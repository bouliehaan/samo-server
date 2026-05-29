package sources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

var (
	ErrPodcastNotFilesystem  = errors.New("podcast is not backed by a local library folder")
	ErrPodcastAlreadyHasFeed = errors.New("podcast already has an rss feed attached")
	ErrFeedURLInUse          = errors.New("rss feed url is already subscribed on another podcast")
)

type podcastShowRow struct {
	ID        string
	LibraryID string
	Path      string
	FolderID  string
	Podcast   catalog.PodcastMetadata
}

func (s *Service) loadPodcastShowRow(ctx context.Context, podcastID string) (podcastShowRow, error) {
	podcastID = strings.TrimSpace(podcastID)
	if podcastID == "" {
		return podcastShowRow{}, ErrNotFound
	}
	var (
		row        podcastShowRow
		podcastRaw sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT p.id, p.library_id, p.path, p.folder_id, p.podcast_json
		FROM podcasts p
		JOIN libraries l ON l.id = p.library_id
		WHERE p.id = ?
		  AND l.path NOT LIKE 'samo://%'`, podcastID).
		Scan(&row.ID, &row.LibraryID, &row.Path, &row.FolderID, &podcastRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return podcastShowRow{}, ErrPodcastNotFilesystem
	}
	if err != nil {
		return podcastShowRow{}, fmt.Errorf("load filesystem podcast: %w", err)
	}
	if podcastRaw.Valid && strings.TrimSpace(podcastRaw.String) != "" {
		_ = json.Unmarshal([]byte(podcastRaw.String), &row.Podcast)
	}
	return row, nil
}

func (s *Service) isFilesystemPodcast(ctx context.Context, podcastID string) (bool, error) {
	podcastID = strings.TrimSpace(podcastID)
	if podcastID == "" {
		return false, nil
	}
	var libraryPath string
	err := s.db.QueryRowContext(ctx, `
		SELECT l.path
		FROM podcasts p
		JOIN libraries l ON l.id = p.library_id
		WHERE p.id = ?`, podcastID).Scan(&libraryPath)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !strings.HasPrefix(strings.TrimSpace(libraryPath), "samo://"), nil
}

func (s *Service) podcastHasFeed(ctx context.Context, podcastID string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM podcast_feeds WHERE podcast_id = ?`, podcastID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Service) feedURLPodcastID(ctx context.Context, feedURL string) (string, bool, error) {
	feedID := podcastFeedID(feedURL)
	var podcastID string
	err := s.db.QueryRowContext(ctx, `SELECT podcast_id FROM podcast_feeds WHERE id = ?`, feedID).Scan(&podcastID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return podcastID, true, nil
}

func (s *Service) loadExistingEpisodesForMatch(ctx context.Context, podcastID string) ([]existingPodcastEpisode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
		  pe.id,
		  pe.title,
		  pe.published_at,
		  pe.season,
		  pe.episode,
		  pe.external_ids_json,
		  pe.enclosure_url,
		  pe.duration_seconds,
		  EXISTS(
		    SELECT 1 FROM media_files mf
		    WHERE mf.episode_id = pe.id AND mf.episode_id != ''
		  ) AS has_local_file
		FROM podcast_episodes pe
		WHERE pe.podcast_id = ?
		ORDER BY pe.published_at DESC`, podcastID)
	if err != nil {
		return nil, fmt.Errorf("list podcast episodes for match: %w", err)
	}
	defer rows.Close()

	episodes := make([]existingPodcastEpisode, 0)
	for rows.Next() {
		var (
			item         existingPodcastEpisode
			publishedRaw sql.NullString
			externalRaw  sql.NullString
		)
		if err := rows.Scan(
			&item.ID,
			&item.Title,
			&publishedRaw,
			&item.Season,
			&item.Episode,
			&externalRaw,
			&item.EnclosureURL,
			&item.DurationSeconds,
			&item.HasLocalFile,
		); err != nil {
			return nil, err
		}
		if publishedRaw.Valid {
			if parsed, err := time.Parse(time.RFC3339, publishedRaw.String); err == nil {
				item.PublishedAt = &parsed
			}
		}
		if externalRaw.Valid && strings.TrimSpace(externalRaw.String) != "" {
			_ = json.Unmarshal([]byte(externalRaw.String), &item.ExternalIDs)
		}
		episodes = append(episodes, item)
	}
	return episodes, rows.Err()
}

func buildHybridEpisodePlans(
	podcastID string,
	libraryID string,
	parsedEpisodes []parsedPodcastEpisode,
	existing []existingPodcastEpisode,
) []catalog.PodcastEpisode {
	used := map[string]struct{}{}
	plans := make([]catalog.PodcastEpisode, 0, len(parsedEpisodes))
	for _, parsedEpisode := range parsedEpisodes {
		if match := findMatchingEpisode(parsedEpisode, existing, used); match != nil {
			used[match.ID] = struct{}{}
			plans = append(plans, mergeRSSIntoExisting(*match, parsedEpisode, podcastID, libraryID))
			continue
		}
		episodeID := podcastEpisodeID(podcastID, parsedEpisode)
		externalIDs := catalog.ExternalIDs{
			FeedGUID: parsedEpisode.GUID,
			URLs:     cleanStringSlice(append([]string{parsedEpisode.Link, parsedEpisode.EnclosureURL}, parsedEpisode.ExternalURLs...)),
		}
		plans = append(plans, catalog.PodcastEpisode{
			ID:              episodeID,
			LibraryID:       libraryID,
			PodcastID:       podcastID,
			Title:           parsedEpisode.Title,
			Subtitle:        parsedEpisode.Subtitle,
			Description:     parsedEpisode.Description,
			PublishedAt:     parsedEpisode.PublishedAt,
			Season:          parsedEpisode.Season,
			Episode:         parsedEpisode.Episode,
			EpisodeType:     parsedEpisode.EpisodeType,
			DurationSeconds: parsedEpisode.DurationSeconds,
			Explicit:        parsedEpisode.Explicit,
			EnclosureURL:    parsedEpisode.EnclosureURL,
			EnclosureType:   parsedEpisode.EnclosureType,
			EnclosureBytes:  parsedEpisode.EnclosureBytes,
			ExternalIDs:     externalIDs,
		})
	}
	return plans
}

func mergePodcastMetadataForHybrid(existing catalog.PodcastMetadata, feedURL string, parsed parsedPodcastFeed) catalog.PodcastMetadata {
	merged := existing
	if strings.TrimSpace(merged.Title) == "" {
		merged.Title = parsed.Title
	}
	if strings.TrimSpace(merged.Author) == "" {
		merged.Author = parsed.Author
	}
	if strings.TrimSpace(merged.Description) == "" {
		merged.Description = parsed.Description
	}
	merged.FeedURL = feedURL
	if strings.TrimSpace(merged.SiteURL) == "" {
		merged.SiteURL = parsed.SiteURL
	}
	if strings.TrimSpace(merged.Language) == "" {
		merged.Language = parsed.Language
	}
	if len(merged.Categories) == 0 {
		merged.Categories = cleanStringSlice(parsed.Categories)
	}
	if strings.TrimSpace(merged.OwnerName) == "" {
		merged.OwnerName = parsed.OwnerName
	}
	if strings.TrimSpace(merged.OwnerEmail) == "" {
		merged.OwnerEmail = parsed.OwnerEmail
	}
	merged.Explicit = merged.Explicit || parsed.Explicit
	merged.ExternalIDs = mergePodcastExternalIDs(merged.ExternalIDs, parsed, feedURL)
	merged.EpisodeCount = 0
	return merged
}

func mergePodcastExternalIDs(existing catalog.ExternalIDs, parsed parsedPodcastFeed, feedURL string) catalog.ExternalIDs {
	merged := existing
	if strings.TrimSpace(merged.FeedGUID) == "" {
		merged.FeedGUID = podcastFeedID(feedURL)
	}
	merged.URLs = uniqueEpisodeStrings(append(
		append([]string{}, merged.URLs...),
		feedURL, parsed.SiteURL,
	))
	return merged
}
