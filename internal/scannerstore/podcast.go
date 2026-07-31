package scannerstore

import (
	"context"
	"fmt"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
)

// UpsertPodcast writes the podcast show row.
//
// The UNIQUE-on-path fallback is the same adoption the audiobook path does —
// see UpsertAudiobook for why keeping the existing row's id matters. Unlike
// audiobooks the caller does not need the resolved id back, so it is not
// returned.
func (s *Store) UpsertPodcast(ctx context.Context, item catalog.PodcastItem) error {
	coverJSON := "{}"
	if item.Cover != nil {
		coverJSON = jsonText(item.Cover)
	}
	var podcastJSON any
	if item.Podcast != nil {
		podcastJSON = jsonText(item.Podcast)
	}

	_, err := s.exec(ctx, `
		INSERT INTO podcasts (
		  id, library_id, path, folder_id, inode, size_bytes, missing, invalid,
		  cover_json, tags_json, genres_json, duration_seconds, progress_json, podcast_json,
		  updated_at, last_scan_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  library_id = excluded.library_id,
		  path = excluded.path,
		  folder_id = excluded.folder_id,
		  inode = excluded.inode,
		  size_bytes = excluded.size_bytes,
		  missing = excluded.missing,
		  invalid = excluded.invalid,
		  cover_json = excluded.cover_json,
		  tags_json = excluded.tags_json,
		  genres_json = excluded.genres_json,
		  duration_seconds = excluded.duration_seconds,
		  podcast_json = excluded.podcast_json,
		  updated_at = CURRENT_TIMESTAMP,
		  last_scan_at = CURRENT_TIMESTAMP`,
		item.ID, item.LibraryID, item.Path, item.FolderID, item.Inode, item.SizeBytes,
		boolInt(item.Missing), boolInt(item.Invalid), coverJSON, jsonText(item.Tags), jsonText(item.Genres),
		item.DurationSeconds, jsonText(item.Progress), podcastJSON)
	if err == nil {
		return nil
	}
	if !storage.IsUniqueViolation(err) {
		return fmt.Errorf("upsert podcast %q: %w", item.ID, err)
	}
	if _, err := s.exec(ctx, `
		UPDATE podcasts
		SET library_id = ?, folder_id = ?, inode = ?, size_bytes = ?, missing = ?, invalid = ?,
		    cover_json = ?, tags_json = ?, genres_json = ?, duration_seconds = ?, podcast_json = ?,
		    updated_at = CURRENT_TIMESTAMP, last_scan_at = CURRENT_TIMESTAMP
		WHERE path = ?`,
		item.LibraryID, item.FolderID, item.Inode, item.SizeBytes,
		boolInt(item.Missing), boolInt(item.Invalid), coverJSON, jsonText(item.Tags), jsonText(item.Genres),
		item.DurationSeconds, podcastJSON, item.Path); err != nil {
		return fmt.Errorf("update podcast by path %q: %w", item.Path, err)
	}
	return nil
}

// UpsertPodcastEpisode writes the episode row. As with audiobooks,
// progress_json is insert-only so a refeed cannot reset a listener's position.
func (s *Store) UpsertPodcastEpisode(ctx context.Context, episode catalog.PodcastEpisode) error {
	_, err := s.exec(ctx, `
		INSERT INTO podcast_episodes (
		  id, library_id, podcast_id, title, subtitle, description, published_at, season, episode,
		  episode_type, duration_seconds, explicit, enclosure_url, enclosure_type, enclosure_bytes,
		  progress_json, external_ids_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
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
		episode.ID, episode.LibraryID, episode.PodcastID, episode.Title, episode.Subtitle, episode.Description,
		timeString(episode.PublishedAt), episode.Season, episode.Episode, episode.EpisodeType, episode.DurationSeconds,
		boolInt(episode.Explicit), episode.EnclosureURL, episode.EnclosureType, episode.EnclosureBytes,
		jsonText(episode.Progress), jsonText(episode.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert podcast episode %q: %w", episode.Title, err)
	}
	return nil
}

// ReplaceEpisodeChapters rewrites an episode's chapter list. Episodes and
// audiobooks keep separate chapter tables so the two scan flows cannot race
// each other over shared rows.
func (s *Store) ReplaceEpisodeChapters(ctx context.Context, episodeID string, chapters []catalog.AudioChapter) error {
	if _, err := s.exec(ctx, `DELETE FROM episode_chapters WHERE episode_id = ?`, episodeID); err != nil {
		return fmt.Errorf("clear episode chapters: %w", err)
	}
	for _, chapter := range chapters {
		if _, err := s.exec(ctx, `
			INSERT INTO episode_chapters (id, episode_id, chapter_index, title, start_seconds, end_seconds, start_ms, end_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			chapter.ID, episodeID, chapter.Index, chapter.Title,
			int(chapter.StartSeconds), int(chapter.EndSeconds), chapter.StartMs(), chapter.EndMs()); err != nil {
			return fmt.Errorf("insert episode chapter %q: %w", chapter.Title, err)
		}
	}
	return nil
}
