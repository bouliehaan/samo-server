package scannerstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// MediaFileOwner identifies which domain row a media_files row belongs to.
//
// At most one owner is populated per file: music tracks set TrackID, audiobook
// files set AudiobookID, and podcast-episode files set both PodcastID and
// EpisodeID (PodcastID denormalized so show-level joins do not have to go
// through the episode).
//
// The same shape describes both what a file's owner should become and what it
// was before a write, which is why the scanner compares two of these to decide
// whether an owner was replaced.
type MediaFileOwner struct {
	AudiobookID string
	PodcastID   string
	TrackID     string
	EpisodeID   string
}

// UpsertMediaFile writes the media_files row keyed by id.
//
// inode is passed in rather than read here: stat'ing the file is the scanner's
// job, and a persistence layer that touches the filesystem is a persistence
// layer you cannot reason about.
//
// missing/missing_detected_at are reset unconditionally — seeing a file is
// exactly what clears a missing mark.
func (s *Store) UpsertMediaFile(ctx context.Context, libraryID string, owner MediaFileOwner, file catalog.AudioFile, inode, trackPID, contentHash string) error {
	_, err := s.exec(ctx, `
		INSERT INTO media_files (
		  id, library_id, audiobook_id, podcast_id, track_id, episode_id, path, relative_path, file_name, inode, size_bytes,
		  modified_at, container, mime_type, codec, codec_profile, metadata_formats_json, bitrate, bit_depth, sample_rate, channels,
		  channel_layout, duration_seconds, duration_ms, checksum, embedded_tags_json, track_pid, content_hash,
		  missing, missing_detected_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, CURRENT_TIMESTAMP)
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
		  duration_ms = excluded.duration_ms,
		  checksum = excluded.checksum,
		  embedded_tags_json = excluded.embedded_tags_json,
		  track_pid = excluded.track_pid,
		  content_hash = excluded.content_hash,
		  missing = 0,
		  missing_detected_at = NULL,
		  updated_at = CURRENT_TIMESTAMP`,
		file.ID, libraryID, nullableString(owner.AudiobookID), nullableString(owner.PodcastID),
		nullableString(owner.TrackID), nullableString(owner.EpisodeID),
		file.Path, file.RelativePath, file.FileName, inode, file.SizeBytes, timeString(file.ModifiedAt),
		file.Container, file.MimeType, file.Codec, file.CodecProfile, jsonText(file.MetadataFormats), file.Bitrate, file.BitDepth, file.SampleRate,
		file.Channels, file.ChannelLayout, file.DurationSeconds, durationMsValue(file), file.Checksum, jsonText(file.EmbeddedTags),
		trackPID, contentHash)
	return err
}

// UpdateMediaFileByPath rewrites the row that already occupies file.Path,
// whatever id it holds. This is the reclaim branch: a UNIQUE violation on path
// means the row exists under an id we did not predict, and updating it in place
// keeps everything referencing it intact.
func (s *Store) UpdateMediaFileByPath(ctx context.Context, libraryID string, owner MediaFileOwner, file catalog.AudioFile, inode, trackPID, contentHash string) error {
	_, err := s.exec(ctx, `
		UPDATE media_files
		SET library_id = ?,
		    audiobook_id = ?,
		    podcast_id = ?,
		    track_id = ?,
		    episode_id = ?,
		    relative_path = ?,
		    file_name = ?,
		    inode = ?,
		    size_bytes = ?,
		    modified_at = ?,
		    container = ?,
		    mime_type = ?,
		    codec = ?,
		    codec_profile = ?,
		    metadata_formats_json = ?,
		    bitrate = ?,
		    bit_depth = ?,
		    sample_rate = ?,
		    channels = ?,
		    channel_layout = ?,
		    duration_seconds = ?,
		    duration_ms = ?,
		    checksum = ?,
		    embedded_tags_json = ?,
		    track_pid = ?,
		    content_hash = ?,
		    missing = 0,
		    missing_detected_at = NULL,
		    updated_at = CURRENT_TIMESTAMP
		WHERE path = ?`,
		libraryID, nullableString(owner.AudiobookID), nullableString(owner.PodcastID),
		nullableString(owner.TrackID), nullableString(owner.EpisodeID),
		file.RelativePath, file.FileName, inode, file.SizeBytes, timeString(file.ModifiedAt),
		file.Container, file.MimeType, file.Codec, file.CodecProfile, jsonText(file.MetadataFormats), file.Bitrate, file.BitDepth, file.SampleRate,
		file.Channels, file.ChannelLayout, file.DurationSeconds, durationMsValue(file), file.Checksum, jsonText(file.EmbeddedTags),
		trackPID, contentHash, file.Path)
	return err
}

// MediaFileIDByPath returns the id of the row at path, or "" when there is
// none. A missing row is an ordinary answer here, not an error.
func (s *Store) MediaFileIDByPath(ctx context.Context, path string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM media_files WHERE path = ?`, path).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// MediaFileOwners returns the owners currently recorded for a file. A file
// that has since vanished reports a zero owner rather than an error.
func (s *Store) MediaFileOwners(ctx context.Context, fileID string) (MediaFileOwner, error) {
	var owner MediaFileOwner
	var trackID, audiobookID, podcastID, episodeID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT track_id, audiobook_id, podcast_id, episode_id
		FROM media_files WHERE id = ?`, fileID).Scan(&trackID, &audiobookID, &podcastID, &episodeID)
	if err == sql.ErrNoRows {
		return owner, nil
	}
	if err != nil {
		return owner, err
	}
	owner.TrackID = trackID.String
	owner.AudiobookID = audiobookID.String
	owner.PodcastID = podcastID.String
	owner.EpisodeID = episodeID.String
	return owner, nil
}

// CountMediaFilesForTrack reports how many files still reference a track.
func (s *Store) CountMediaFilesForTrack(ctx context.Context, trackID string) (int, error) {
	var refs int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_files WHERE track_id = ?`, trackID).Scan(&refs); err != nil {
		return 0, fmt.Errorf("count media_files for track %q: %w", trackID, err)
	}
	return refs, nil
}

// DeleteMusicTrack removes a track row.
func (s *Store) DeleteMusicTrack(ctx context.Context, trackID string) error {
	if _, err := s.exec(ctx, `DELETE FROM music_tracks WHERE id = ?`, trackID); err != nil {
		return fmt.Errorf("delete orphan track %q: %w", trackID, err)
	}
	return nil
}

// CountMediaFilesForEpisode reports how many files still reference an episode.
func (s *Store) CountMediaFilesForEpisode(ctx context.Context, episodeID string) (int, error) {
	var refs int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_files WHERE episode_id = ?`, episodeID).Scan(&refs); err != nil {
		return 0, fmt.Errorf("count media_files for episode %q: %w", episodeID, err)
	}
	return refs, nil
}

// DeletePodcastEpisode removes an episode row.
func (s *Store) DeletePodcastEpisode(ctx context.Context, episodeID string) error {
	if _, err := s.exec(ctx, `DELETE FROM podcast_episodes WHERE id = ?`, episodeID); err != nil {
		return fmt.Errorf("delete orphan episode %q: %w", episodeID, err)
	}
	return nil
}

// durationMsValue returns the exact millisecond duration to persist, falling
// back to whole seconds for files probed before ffprobe filled DurationMs.
func durationMsValue(file catalog.AudioFile) int64 {
	if file.DurationMs > 0 {
		return file.DurationMs
	}
	return int64(file.DurationSeconds) * 1000
}
