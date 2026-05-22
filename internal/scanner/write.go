package scanner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jakedebus/samo-server/internal/catalog"
)

func (s *Scanner) upsertMusicArtist(ctx context.Context, artist catalog.MusicArtist) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO music_artists (id, name, sort_name, genres_json, external_ids_json, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  sort_name = excluded.sort_name,
		  genres_json = excluded.genres_json,
		  external_ids_json = excluded.external_ids_json,
		  updated_at = CURRENT_TIMESTAMP`,
		artist.ID, artist.Name, artist.SortName, jsonText(artist.Genres), jsonText(artist.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert music artist %q: %w", artist.Name, err)
	}
	return nil
}

func (s *Scanner) upsertMusicAlbum(ctx context.Context, album catalog.MusicAlbum) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO music_albums (
		  id, title, sort_title, version, display_artist, release_date, original_release_date, release_year, release_type,
		  release_status, compilation, record_label, catalog_number, barcode, genres_json, styles_json, moods_json,
		  tags_json, images_json, external_ids_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  title = excluded.title,
		  sort_title = excluded.sort_title,
		  version = excluded.version,
		  display_artist = excluded.display_artist,
		  release_date = excluded.release_date,
		  original_release_date = excluded.original_release_date,
		  release_year = excluded.release_year,
		  release_type = excluded.release_type,
		  release_status = excluded.release_status,
		  compilation = excluded.compilation,
		  record_label = excluded.record_label,
		  catalog_number = excluded.catalog_number,
		  barcode = excluded.barcode,
		  genres_json = excluded.genres_json,
		  styles_json = excluded.styles_json,
		  moods_json = excluded.moods_json,
		  tags_json = excluded.tags_json,
		  images_json = excluded.images_json,
		  external_ids_json = excluded.external_ids_json,
		  updated_at = CURRENT_TIMESTAMP`,
		album.ID, album.Title, album.SortTitle, album.Version, album.DisplayArtist, album.ReleaseDate, album.OriginalReleaseDate, album.ReleaseYear,
		album.ReleaseType, album.ReleaseStatus, boolInt(album.Compilation), album.RecordLabel, album.CatalogNumber, album.Barcode,
		jsonText(album.Genres), jsonText(album.Styles), jsonText(album.Moods), jsonText(album.Tags),
		jsonText(album.Images), jsonText(album.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert music album %q: %w", album.Title, err)
	}
	return nil
}

func (s *Scanner) setAlbumArtists(ctx context.Context, albumID string, artists []catalog.MusicArtist) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM music_album_artists WHERE album_id = ?`, albumID); err != nil {
		return fmt.Errorf("clear album artists: %w", err)
	}
	for index, artist := range artists {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO music_album_artists (album_id, artist_id, position)
			VALUES (?, ?, ?)`,
			albumID, artist.ID, index); err != nil {
			return fmt.Errorf("insert album artist: %w", err)
		}
	}
	return nil
}

func (s *Scanner) upsertMusicTrack(ctx context.Context, track catalog.MusicTrack) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO music_tracks (
		  id, title, sort_title, subtitle, display_artist, album_id, album_title, disc_number, track_number, total_discs,
		  total_tracks, release_date, release_year, genres_json, moods_json, tags_json, duration_seconds,
		  explicit, bpm, musical_key, comment, lyrics_json, images_json, external_ids_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  title = excluded.title,
		  sort_title = excluded.sort_title,
		  subtitle = excluded.subtitle,
		  display_artist = excluded.display_artist,
		  album_id = excluded.album_id,
		  album_title = excluded.album_title,
		  disc_number = excluded.disc_number,
		  track_number = excluded.track_number,
		  total_discs = excluded.total_discs,
		  total_tracks = excluded.total_tracks,
		  release_date = excluded.release_date,
		  release_year = excluded.release_year,
		  genres_json = excluded.genres_json,
		  moods_json = excluded.moods_json,
		  tags_json = excluded.tags_json,
		  duration_seconds = excluded.duration_seconds,
		  explicit = excluded.explicit,
		  bpm = excluded.bpm,
		  musical_key = excluded.musical_key,
		  comment = excluded.comment,
		  lyrics_json = excluded.lyrics_json,
		  images_json = excluded.images_json,
		  external_ids_json = excluded.external_ids_json,
		  updated_at = CURRENT_TIMESTAMP`,
		track.ID, track.Title, track.SortTitle, track.Subtitle, track.DisplayArtist, nullableString(track.AlbumID), track.AlbumTitle,
		track.DiscNumber, track.TrackNumber, track.TotalDiscs, track.TotalTracks, track.ReleaseDate, track.ReleaseYear,
		jsonText(track.Genres), jsonText(track.Moods), jsonText(track.Tags), track.DurationSeconds, boolInt(track.Explicit),
		track.BPM, track.Key, track.Comment, jsonText(track.Lyrics), jsonText(track.Images), jsonText(track.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert music track %q: %w", track.Title, err)
	}
	return nil
}

func (s *Scanner) setTrackArtists(ctx context.Context, trackID string, artists []catalog.MusicArtist) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM music_track_artists WHERE track_id = ?`, trackID); err != nil {
		return fmt.Errorf("clear track artists: %w", err)
	}
	for index, artist := range artists {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO music_track_artists (track_id, artist_id, role, position)
			VALUES (?, ?, 'artist', ?)`,
			trackID, artist.ID, index); err != nil {
			return fmt.Errorf("insert track artist: %w", err)
		}
	}
	return nil
}

func (s *Scanner) upsertShelfItem(ctx context.Context, item catalog.ShelfItem) error {
	coverJSON := "{}"
	if item.Cover != nil {
		coverJSON = jsonText(item.Cover)
	}
	var bookJSON any
	if item.Book != nil {
		bookJSON = jsonText(item.Book)
	}
	var podcastJSON any
	if item.Podcast != nil {
		podcastJSON = jsonText(item.Podcast)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO shelf_items (
		  id, library_id, media_type, media_kind, path, folder_id, inode, size_bytes, missing, invalid,
		  cover_json, tags_json, genres_json, duration_seconds, progress_json, book_json, podcast_json,
		  updated_at, last_scan_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  library_id = excluded.library_id,
		  media_type = excluded.media_type,
		  media_kind = excluded.media_kind,
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
		  book_json = excluded.book_json,
		  podcast_json = excluded.podcast_json,
		  updated_at = CURRENT_TIMESTAMP,
		  last_scan_at = CURRENT_TIMESTAMP`,
		item.ID, item.LibraryID, item.MediaType, item.MediaKind, item.Path, item.FolderID, item.Inode, item.SizeBytes,
		boolInt(item.Missing), boolInt(item.Invalid), coverJSON, jsonText(item.Tags), jsonText(item.Genres),
		item.DurationSeconds, jsonText(item.Progress), bookJSON, podcastJSON)
	if err != nil {
		return fmt.Errorf("upsert shelf item %q: %w", item.ID, err)
	}
	return nil
}

func (s *Scanner) upsertShelfAuthor(ctx context.Context, author catalog.ShelfAuthor) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO shelf_authors (id, name, sort_name, description, images_json, external_ids_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  sort_name = excluded.sort_name,
		  description = excluded.description,
		  images_json = excluded.images_json,
		  external_ids_json = excluded.external_ids_json`,
		author.ID, author.Name, author.SortName, author.Description, jsonText(author.Images), jsonText(author.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert shelf author %q: %w", author.Name, err)
	}
	return nil
}

func (s *Scanner) setShelfItemAuthors(ctx context.Context, itemID string, authors []catalog.Contributor) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM shelf_item_authors WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("clear shelf item authors: %w", err)
	}
	for index, author := range authors {
		if author.ID == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO shelf_item_authors (item_id, author_id, role, position)
			VALUES (?, ?, ?, ?)`,
			itemID, author.ID, author.Role, index); err != nil {
			return fmt.Errorf("insert shelf item author: %w", err)
		}
	}
	return nil
}

func (s *Scanner) upsertShelfSeries(ctx context.Context, series catalog.ShelfSeries) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO shelf_series (id, name, description, authors_json, item_ids_json, external_ids_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  description = excluded.description,
		  authors_json = excluded.authors_json,
		  item_ids_json = excluded.item_ids_json,
		  external_ids_json = excluded.external_ids_json`,
		series.ID, series.Name, series.Description, jsonText(series.Authors), jsonText(series.ItemIDs), jsonText(series.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert shelf series %q: %w", series.Name, err)
	}
	return nil
}

func (s *Scanner) setShelfItemSeries(ctx context.Context, itemID string, series []catalog.SeriesRef) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM shelf_item_series WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("clear shelf item series: %w", err)
	}
	for _, entry := range series {
		if entry.ID == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO shelf_item_series (item_id, series_id, sequence, sequence_text)
			VALUES (?, ?, ?, ?)`,
			itemID, entry.ID, entry.Sequence, entry.SequenceText); err != nil {
			return fmt.Errorf("insert shelf item series: %w", err)
		}
	}
	return nil
}

func (s *Scanner) upsertPodcastEpisode(ctx context.Context, episode catalog.PodcastEpisode) error {
	_, err := s.db.ExecContext(ctx, `
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

func (s *Scanner) replaceChapters(ctx context.Context, itemID string, episodeID string, chapters []catalog.AudioChapter) error {
	if episodeID != "" {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM shelf_chapters WHERE episode_id = ?`, episodeID); err != nil {
			return fmt.Errorf("clear episode chapters: %w", err)
		}
	} else {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM shelf_chapters WHERE item_id = ? AND episode_id IS NULL`, itemID); err != nil {
			return fmt.Errorf("clear item chapters: %w", err)
		}
	}

	for _, chapter := range chapters {
		chapter.ID = stableID("chapter", itemID, episodeID, fmt.Sprint(chapter.Index), chapter.Title, fmt.Sprint(chapter.StartSeconds))
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO shelf_chapters (id, item_id, episode_id, chapter_index, title, start_seconds, end_seconds)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			chapter.ID, itemID, nullableString(episodeID), chapter.Index, chapter.Title, chapter.StartSeconds, chapter.EndSeconds); err != nil {
			return fmt.Errorf("insert chapter %q: %w", chapter.Title, err)
		}
	}
	return nil
}

func (s *Scanner) upsertAudioFile(ctx context.Context, libraryID string, owner audioFileOwner, file catalog.AudioFile) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_files (
		  id, library_id, item_id, track_id, episode_id, path, relative_path, file_name, inode, size_bytes,
		  modified_at, container, mime_type, codec, codec_profile, metadata_formats_json, bitrate, bit_depth, sample_rate, channels,
		  channel_layout, duration_seconds, checksum, embedded_tags_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  library_id = excluded.library_id,
		  item_id = excluded.item_id,
		  track_id = excluded.track_id,
		  episode_id = excluded.episode_id,
		  path = excluded.path,
		  relative_path = excluded.relative_path,
		  file_name = excluded.file_name,
		  inode = excluded.inode,
		  size_bytes = excluded.size_bytes,
		  modified_at = excluded.modified_at,
		  container = excluded.container,
		  mime_type = excluded.mime_type,
		  codec = excluded.codec,
		  codec_profile = excluded.codec_profile,
		  metadata_formats_json = excluded.metadata_formats_json,
		  bitrate = excluded.bitrate,
		  bit_depth = excluded.bit_depth,
		  sample_rate = excluded.sample_rate,
		  channels = excluded.channels,
		  channel_layout = excluded.channel_layout,
		  duration_seconds = excluded.duration_seconds,
		  checksum = excluded.checksum,
		  embedded_tags_json = excluded.embedded_tags_json,
		  updated_at = CURRENT_TIMESTAMP`,
		file.ID, libraryID, nullableString(owner.ItemID), nullableString(owner.TrackID), nullableString(owner.EpisodeID),
		file.Path, file.RelativePath, file.FileName, fileInode(file.Path), file.SizeBytes, timeString(file.ModifiedAt),
		file.Container, file.MimeType, file.Codec, file.CodecProfile, jsonText(file.MetadataFormats), file.Bitrate, file.BitDepth, file.SampleRate,
		file.Channels, file.ChannelLayout, file.DurationSeconds, file.Checksum, jsonText(file.EmbeddedTags))
	if err != nil {
		return fmt.Errorf("upsert audio file %q: %w", file.Path, err)
	}
	return nil
}

type audioFileOwner struct {
	ItemID    string
	TrackID   string
	EpisodeID string
}

func (s *Scanner) upsertGenre(ctx context.Context, kind string, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO genres (name, kind)
		VALUES (?, ?)
		ON CONFLICT(name, kind) DO NOTHING`,
		name, kind)
	return err
}

func (s *Scanner) refreshStats(ctx context.Context) error {
	statements := []string{
		`UPDATE music_albums
		 SET track_count = (SELECT COUNT(*) FROM music_tracks WHERE album_id = music_albums.id),
		     duration_seconds = COALESCE((SELECT SUM(duration_seconds) FROM music_tracks WHERE album_id = music_albums.id), 0),
		     disc_count = COALESCE((SELECT MAX(disc_number) FROM music_tracks WHERE album_id = music_albums.id), 0)`,
		`UPDATE music_artists
		 SET track_count = COALESCE((SELECT COUNT(DISTINCT track_id) FROM music_track_artists WHERE artist_id = music_artists.id), 0),
		     album_count = COALESCE((SELECT COUNT(DISTINCT album_id) FROM music_album_artists WHERE artist_id = music_artists.id), 0),
		     duration_seconds = COALESCE((
		       SELECT SUM(t.duration_seconds)
		       FROM music_tracks t
		       JOIN music_track_artists ta ON ta.track_id = t.id
		       WHERE ta.artist_id = music_artists.id
		     ), 0)`,
		`UPDATE shelf_items
		 SET duration_seconds = COALESCE((SELECT SUM(duration_seconds) FROM media_files WHERE item_id = shelf_items.id), duration_seconds)`,
		`UPDATE shelf_authors
		 SET item_count = COALESCE((SELECT COUNT(DISTINCT item_id) FROM shelf_item_authors WHERE author_id = shelf_authors.id), 0),
		     duration_seconds = COALESCE((
		       SELECT SUM(si.duration_seconds)
		       FROM shelf_items si
		       JOIN shelf_item_authors sia ON sia.item_id = si.id
		       WHERE sia.author_id = shelf_authors.id
		     ), 0)`,
		`UPDATE shelf_series
		 SET item_count = COALESCE((SELECT COUNT(DISTINCT item_id) FROM shelf_item_series WHERE series_id = shelf_series.id), 0),
		     duration_seconds = COALESCE((
		       SELECT SUM(si.duration_seconds)
		       FROM shelf_items si
		       JOIN shelf_item_series sis ON sis.item_id = si.id
		       WHERE sis.series_id = shelf_series.id
		     ), 0)`,
		`UPDATE libraries
		 SET item_count = CASE
		   WHEN kind = 'music' THEN COALESCE((SELECT COUNT(*) FROM media_files WHERE library_id = libraries.id), 0)
		   WHEN kind = 'shelf' THEN COALESCE((SELECT COUNT(*) FROM shelf_items WHERE library_id = libraries.id), 0)
		   ELSE item_count
		 END`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("refresh scanner stats: %w", err)
		}
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func timeString(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}
