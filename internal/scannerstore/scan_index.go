package scannerstore

import (
	"context"
	"database/sql"
	"fmt"
)

// IndexedFile is what a quick scan needs to decide whether a path changed:
// its checksum, and the owners it is already attached to.
type IndexedFile struct {
	Checksum    string
	TrackID     string
	AudiobookID string
	PodcastID   string
	EpisodeID   string
}

// CachedProbe is the technical metadata already stored for a file, reused by a
// repair scan instead of re-probing every file on disk.
type CachedProbe struct {
	EmbeddedTagsJSON    string
	Checksum            string
	RelativePath        string
	FileName            string
	Container           string
	MimeType            string
	Codec               string
	CodecProfile        string
	MetadataFormatsJSON string
	ChannelLayout       string
	DurationSeconds     int
	Bitrate             int
	BitDepth            int
	SampleRate          int
	Channels            int
	SizeBytes           int64
	ModifiedAt          sql.NullString
}

// FileIndex returns every indexed file in the library, keyed by path.
func (s *Store) FileIndex(ctx context.Context, libraryID string) (map[string]IndexedFile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT path, checksum, track_id, audiobook_id, podcast_id, episode_id
		FROM media_files
		WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("load media file index: %w", err)
	}
	defer rows.Close()

	index := map[string]IndexedFile{}
	for rows.Next() {
		var path string
		var entry IndexedFile
		var trackID, audiobookID, podcastID, episodeID sql.NullString
		if err := rows.Scan(&path, &entry.Checksum, &trackID, &audiobookID, &podcastID, &episodeID); err != nil {
			return nil, fmt.Errorf("scan media file index row: %w", err)
		}
		entry.TrackID = trackID.String
		entry.AudiobookID = audiobookID.String
		entry.PodcastID = podcastID.String
		entry.EpisodeID = episodeID.String
		index[path] = entry
	}
	return index, rows.Err()
}

// CachedProbeForOwner reads the stored technical metadata for a file, provided
// it is still attached to an owner of the given kind.
//
// ownerColumn names a media_files column and is interpolated into the SQL, so
// it must come from a fixed set the caller controls — never from a request.
func (s *Store) CachedProbeForOwner(ctx context.Context, libraryID, path, ownerColumn string) (CachedProbe, error) {
	var p CachedProbe
	query := `
		SELECT embedded_tags_json, checksum, relative_path, file_name, container, mime_type, codec,
		       codec_profile, metadata_formats_json, channel_layout, duration_seconds, bitrate,
		       bit_depth, sample_rate, channels, size_bytes, modified_at
		FROM media_files
		WHERE library_id = ? AND path = ? AND ` + ownerColumn + ` IS NOT NULL AND ` + ownerColumn + ` != ''`
	err := s.db.QueryRowContext(ctx, query, libraryID, path).Scan(
		&p.EmbeddedTagsJSON, &p.Checksum, &p.RelativePath, &p.FileName, &p.Container, &p.MimeType, &p.Codec,
		&p.CodecProfile, &p.MetadataFormatsJSON, &p.ChannelLayout, &p.DurationSeconds, &p.Bitrate,
		&p.BitDepth, &p.SampleRate, &p.Channels, &p.SizeBytes, &p.ModifiedAt,
	)
	return p, err
}
