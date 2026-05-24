package scanner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func (s *Scanner) upsertMusicArtist(ctx context.Context, artist catalog.MusicArtist) error {
	if s.overrideIndex != nil {
		var err error
		artist, err = s.overrideIndex.GuardMusicArtist(ctx, s.db, artist)
		if err != nil {
			return err
		}
	}
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
	if s.overrideIndex != nil {
		var err error
		album, err = s.overrideIndex.GuardMusicAlbum(ctx, s.db, album)
		if err != nil {
			return err
		}
	}
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
	if s.overrideIndex != nil && s.overrideIndex.HasField(catalog.OverrideKindMusicAlbum, albumID, "artists") {
		return nil
	}
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
	if s.overrideIndex != nil {
		var err error
		track, err = s.overrideIndex.GuardMusicTrack(ctx, s.db, track)
		if err != nil {
			return err
		}
	}
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
	if s.overrideIndex != nil && s.overrideIndex.HasField(catalog.OverrideKindMusicTrack, trackID, "artists") {
		return nil
	}
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

func (s *Scanner) upsertAudiobook(ctx context.Context, item catalog.AudiobookItem) error {
	if s.overrideIndex != nil {
		var err error
		item, err = s.overrideIndex.GuardAudiobook(ctx, s.db, item)
		if err != nil {
			return err
		}
	}
	coverJSON := "{}"
	if item.Cover != nil {
		coverJSON = jsonText(item.Cover)
	}
	var bookJSON any
	if item.Book != nil {
		bookJSON = jsonText(item.Book)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audiobooks (
		  id, library_id, path, folder_id, inode, size_bytes, missing, invalid,
		  cover_json, tags_json, genres_json, duration_seconds, progress_json, book_json,
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
		  book_json = excluded.book_json,
		  updated_at = CURRENT_TIMESTAMP,
		  last_scan_at = CURRENT_TIMESTAMP`,
		item.ID, item.LibraryID, item.Path, item.FolderID, item.Inode, item.SizeBytes,
		boolInt(item.Missing), boolInt(item.Invalid), coverJSON, jsonText(item.Tags), jsonText(item.Genres),
		item.DurationSeconds, jsonText(item.Progress), bookJSON)
	if err == nil {
		return nil
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		return fmt.Errorf("upsert audiobook %q: %w", item.ID, err)
	}
	// Path UNIQUE collision — a row exists at this path under a different
	// id (e.g. left over from an earlier scan when the library_id hashed
	// differently). Preserve the existing id and update everything else
	// against it.
	_, err = s.db.ExecContext(ctx, `
		UPDATE audiobooks
		SET library_id = ?, folder_id = ?, inode = ?, size_bytes = ?, missing = ?, invalid = ?,
		    cover_json = ?, tags_json = ?, genres_json = ?, duration_seconds = ?, book_json = ?,
		    updated_at = CURRENT_TIMESTAMP, last_scan_at = CURRENT_TIMESTAMP
		WHERE path = ?`,
		item.LibraryID, item.FolderID, item.Inode, item.SizeBytes,
		boolInt(item.Missing), boolInt(item.Invalid), coverJSON, jsonText(item.Tags), jsonText(item.Genres),
		item.DurationSeconds, bookJSON, item.Path)
	if err != nil {
		return fmt.Errorf("update audiobook by path %q: %w", item.Path, err)
	}
	return nil
}

func (s *Scanner) upsertPodcast(ctx context.Context, item catalog.PodcastItem) error {
	if s.overrideIndex != nil {
		var err error
		item, err = s.overrideIndex.GuardPodcast(ctx, s.db, item)
		if err != nil {
			return err
		}
	}
	coverJSON := "{}"
	if item.Cover != nil {
		coverJSON = jsonText(item.Cover)
	}
	var podcastJSON any
	if item.Podcast != nil {
		podcastJSON = jsonText(item.Podcast)
	}

	_, err := s.db.ExecContext(ctx, `
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
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		return fmt.Errorf("upsert podcast %q: %w", item.ID, err)
	}
	// Path UNIQUE collision — see upsertAudiobook for context.
	_, err = s.db.ExecContext(ctx, `
		UPDATE podcasts
		SET library_id = ?, folder_id = ?, inode = ?, size_bytes = ?, missing = ?, invalid = ?,
		    cover_json = ?, tags_json = ?, genres_json = ?, duration_seconds = ?, podcast_json = ?,
		    updated_at = CURRENT_TIMESTAMP, last_scan_at = CURRENT_TIMESTAMP
		WHERE path = ?`,
		item.LibraryID, item.FolderID, item.Inode, item.SizeBytes,
		boolInt(item.Missing), boolInt(item.Invalid), coverJSON, jsonText(item.Tags), jsonText(item.Genres),
		item.DurationSeconds, podcastJSON, item.Path)
	if err != nil {
		return fmt.Errorf("update podcast by path %q: %w", item.Path, err)
	}
	return nil
}

// upsertContributor writes a row into the `contributors` table. Used by
// the audiobook scanner to ensure authors / narrators exist before we link
// them. Idempotent.
func (s *Scanner) upsertContributor(ctx context.Context, contributor catalog.Contributor) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO contributors (id, name, sort_name, description, images_json, external_ids_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  sort_name = excluded.sort_name,
		  description = excluded.description,
		  images_json = excluded.images_json,
		  external_ids_json = excluded.external_ids_json`,
		contributor.ID, contributor.Name, contributor.SortName, contributor.Description,
		jsonText(contributor.Images), jsonText(contributor.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert contributor %q: %w", contributor.Name, err)
	}
	return nil
}

// setAudiobookContributors replaces an audiobook's contributor list
// (authors + narrators in one slice, distinguished by role). This is the
// canonical write path — it ALWAYS upserts every contributor row first
// before inserting the junction row, which closes the "FK constraint
// failed" hole that bit us when narrators were never written to
// shelf_authors before being linked.
func (s *Scanner) setAudiobookContributors(ctx context.Context, audiobookID string, contributors []catalog.ContributorRef) error {
	if s.overrideIndex != nil {
		if s.overrideIndex.HasField(catalog.OverrideKindAudiobook, audiobookID, "authors") ||
			s.overrideIndex.HasField(catalog.OverrideKindAudiobook, audiobookID, "narrators") {
			return nil
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM audiobook_contributors WHERE audiobook_id = ?`, audiobookID); err != nil {
		return fmt.Errorf("clear audiobook contributors: %w", err)
	}
	for _, ref := range contributors {
		if ref.ID == "" {
			continue
		}
		if err := s.upsertContributor(ctx, catalog.Contributor{
			ID:       ref.ID,
			Name:     ref.Name,
			SortName: ref.SortName,
		}); err != nil {
			return err
		}
	}
	for index, ref := range contributors {
		if ref.ID == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO audiobook_contributors (audiobook_id, contributor_id, role, position)
			VALUES (?, ?, ?, ?)`,
			audiobookID, ref.ID, ref.Role, index); err != nil {
			return fmt.Errorf("insert audiobook contributor: %w", err)
		}
	}
	return nil
}

func (s *Scanner) upsertSeries(ctx context.Context, series catalog.Series) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO series (id, name, description, authors_json, item_ids_json, external_ids_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  description = excluded.description,
		  authors_json = excluded.authors_json,
		  item_ids_json = excluded.item_ids_json,
		  external_ids_json = excluded.external_ids_json`,
		series.ID, series.Name, series.Description, jsonText(series.Authors),
		jsonText(series.AudiobookIDs), jsonText(series.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert series %q: %w", series.Name, err)
	}
	return nil
}

func (s *Scanner) setAudiobookSeries(ctx context.Context, audiobookID string, series []catalog.SeriesRef) error {
	if s.overrideIndex != nil && s.overrideIndex.HasField(catalog.OverrideKindAudiobook, audiobookID, "series") {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM audiobook_series WHERE audiobook_id = ?`, audiobookID); err != nil {
		return fmt.Errorf("clear audiobook series: %w", err)
	}
	for _, entry := range series {
		if entry.ID == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO audiobook_series (audiobook_id, series_id, sequence, sequence_text)
			VALUES (?, ?, ?, ?)`,
			audiobookID, entry.ID, entry.Sequence, entry.SequenceText); err != nil {
			return fmt.Errorf("insert audiobook series: %w", err)
		}
	}
	return nil
}

func (s *Scanner) upsertPodcastEpisode(ctx context.Context, episode catalog.PodcastEpisode) error {
	if s.overrideIndex != nil {
		var err error
		episode, err = s.overrideIndex.GuardPodcastEpisode(ctx, s.db, episode)
		if err != nil {
			return err
		}
	}
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

// replaceAudiobookChapters rewrites the audiobook's chapter list. Audio
// books and podcast episodes have separate chapter tables now (was: shared
// shelf_chapters) so the two flows do not race each other.
func (s *Scanner) replaceAudiobookChapters(ctx context.Context, audiobookID string, chapters []catalog.AudioChapter) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM audiobook_chapters WHERE audiobook_id = ?`, audiobookID); err != nil {
		return fmt.Errorf("clear audiobook chapters: %w", err)
	}
	for _, chapter := range chapters {
		chapter.ID = stableID("chapter", "audiobook", audiobookID, fmt.Sprint(chapter.Index), chapter.Title, fmt.Sprint(chapter.StartSeconds))
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO audiobook_chapters (id, audiobook_id, chapter_index, title, start_seconds, end_seconds)
			VALUES (?, ?, ?, ?, ?, ?)`,
			chapter.ID, audiobookID, chapter.Index, chapter.Title, chapter.StartSeconds, chapter.EndSeconds); err != nil {
			return fmt.Errorf("insert audiobook chapter %q: %w", chapter.Title, err)
		}
	}
	return nil
}

func (s *Scanner) replaceEpisodeChapters(ctx context.Context, episodeID string, chapters []catalog.AudioChapter) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM episode_chapters WHERE episode_id = ?`, episodeID); err != nil {
		return fmt.Errorf("clear episode chapters: %w", err)
	}
	for _, chapter := range chapters {
		chapter.ID = stableID("chapter", "episode", episodeID, fmt.Sprint(chapter.Index), chapter.Title, fmt.Sprint(chapter.StartSeconds))
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO episode_chapters (id, episode_id, chapter_index, title, start_seconds, end_seconds)
			VALUES (?, ?, ?, ?, ?, ?)`,
			chapter.ID, episodeID, chapter.Index, chapter.Title, chapter.StartSeconds, chapter.EndSeconds); err != nil {
			return fmt.Errorf("insert episode chapter %q: %w", chapter.Title, err)
		}
	}
	return nil
}

func (s *Scanner) upsertAudioFile(ctx context.Context, libraryID string, owner audioFileOwner, file catalog.AudioFile) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_files (
		  id, library_id, audiobook_id, podcast_id, track_id, episode_id, path, relative_path, file_name, inode, size_bytes,
		  modified_at, container, mime_type, codec, codec_profile, metadata_formats_json, bitrate, bit_depth, sample_rate, channels,
		  channel_layout, duration_seconds, checksum, embedded_tags_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  library_id = excluded.library_id,
		  audiobook_id = excluded.audiobook_id,
		  podcast_id = excluded.podcast_id,
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
		file.ID, libraryID, nullableString(owner.AudiobookID), nullableString(owner.PodcastID),
		nullableString(owner.TrackID), nullableString(owner.EpisodeID),
		file.Path, file.RelativePath, file.FileName, fileInode(file.Path), file.SizeBytes, timeString(file.ModifiedAt),
		file.Container, file.MimeType, file.Codec, file.CodecProfile, jsonText(file.MetadataFormats), file.Bitrate, file.BitDepth, file.SampleRate,
		file.Channels, file.ChannelLayout, file.DurationSeconds, file.Checksum, jsonText(file.EmbeddedTags))
	if err != nil {
		return fmt.Errorf("upsert audio file %q: %w", file.Path, err)
	}
	if s.activeScan != nil {
		s.activeScan.seeFile(file.Path)
	}
	return nil
}

// audioFileOwner identifies which domain row a media_files row belongs to.
// At most one of the four IDs is populated. Music tracks set TrackID;
// audiobook files set AudiobookID; podcast-episode files set both
// PodcastID and EpisodeID (PodcastID is denormalized for fast joins).
type audioFileOwner struct {
	AudiobookID string
	PodcastID   string
	TrackID     string
	EpisodeID   string
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

// RefreshStats recomputes the aggregate count and duration columns that
// catalog reads project to clients (libraries.item_count, music_artists.album_count,
// music_artists.track_count, etc.). The scanner runs this at the tail of every
// scan; call it directly at startup to repair drifted counts on existing data
// (e.g. after migration 016 renamed shelf libraries, or after Cursor's
// refactor changed schemas before the scanner had a chance to re-run).
func (s *Scanner) RefreshStats(ctx context.Context) error {
	return s.refreshStats(ctx)
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
		`UPDATE audiobooks
		 SET duration_seconds = COALESCE((SELECT SUM(duration_seconds) FROM media_files WHERE audiobook_id = audiobooks.id), duration_seconds)`,
		`UPDATE podcasts
		 SET duration_seconds = COALESCE((SELECT SUM(duration_seconds) FROM media_files WHERE podcast_id = podcasts.id), duration_seconds)`,
		`UPDATE contributors
		 SET item_count = COALESCE((SELECT COUNT(DISTINCT audiobook_id) FROM audiobook_contributors WHERE contributor_id = contributors.id), 0),
		     duration_seconds = COALESCE((
		       SELECT SUM(a.duration_seconds)
		       FROM audiobooks a
		       JOIN audiobook_contributors ac ON ac.audiobook_id = a.id
		       WHERE ac.contributor_id = contributors.id
		     ), 0)`,
		`UPDATE series
		 SET item_count = COALESCE((SELECT COUNT(DISTINCT audiobook_id) FROM audiobook_series WHERE series_id = series.id), 0),
		     duration_seconds = COALESCE((
		       SELECT SUM(a.duration_seconds)
		       FROM audiobooks a
		       JOIN audiobook_series aas ON aas.audiobook_id = a.id
		       WHERE aas.series_id = series.id
		     ), 0)`,
		// libraries.item_count surfaces on the home dashboard and the
		// settings "attached libraries" panel. Count what a human would
		// count for that kind:
		//   - music:     distinct music_tracks
		//   - audiobook: rows in audiobooks
		//   - podcast:   rows in podcasts (the show, not episodes)
		//   - mixed:     all three summed (any combination the scanner
		//                discovered in this root)
		// This statement runs every Scan, including partial-failure
		// scans, so counts stay current even when one library throws.
		`UPDATE libraries
		 SET item_count = CASE
		   WHEN kind = 'music' THEN COALESCE((
		     SELECT COUNT(DISTINCT t.id)
		     FROM music_tracks t
		     JOIN media_files mf ON mf.track_id = t.id
		     WHERE mf.library_id = libraries.id
		   ), 0)
		   WHEN kind = 'audiobook' THEN COALESCE((SELECT COUNT(*) FROM audiobooks WHERE library_id = libraries.id), 0)
		   WHEN kind = 'podcast' THEN COALESCE((SELECT COUNT(*) FROM podcasts WHERE library_id = libraries.id), 0)
		   WHEN kind = 'mixed' THEN COALESCE((
		     SELECT COUNT(DISTINCT t.id)
		     FROM music_tracks t
		     JOIN media_files mf ON mf.track_id = t.id
		     WHERE mf.library_id = libraries.id
		   ), 0)
		     + COALESCE((SELECT COUNT(*) FROM audiobooks WHERE library_id = libraries.id), 0)
		     + COALESCE((SELECT COUNT(*) FROM podcasts WHERE library_id = libraries.id), 0)
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
