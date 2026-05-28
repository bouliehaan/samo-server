package scanner

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func (s *Scanner) loadCachedMusicProbe(ctx context.Context, libraryID, path string) (probeInfo, error) {
	return s.loadCachedMediaProbe(ctx, libraryID, path, "track_id")
}

// loadCachedMediaProbe rebuilds probe metadata from a previous scan row when
// ffprobe fails (unsupported binary flags, corrupt file, etc.).
func (s *Scanner) loadCachedMediaProbe(ctx context.Context, libraryID, path, ownerColumn string) (probeInfo, error) {
	if s.db == nil {
		return probeInfo{}, sql.ErrNoRows
	}
	path = strings.TrimSpace(path)
	libraryID = strings.TrimSpace(libraryID)
	ownerColumn = strings.TrimSpace(ownerColumn)
	if path == "" || libraryID == "" || ownerColumn == "" {
		return probeInfo{}, sql.ErrNoRows
	}
	switch ownerColumn {
	case "track_id", "audiobook_id", "episode_id":
	default:
		return probeInfo{}, sql.ErrNoRows
	}

	var embeddedJSON string
	var checksum string
	var relativePath string
	var fileName string
	var container string
	var mimeType string
	var codec string
	var codecProfile string
	var metadataFormatsJSON string
	var channelLayout string
	var durationSeconds int
	var bitrate int
	var bitDepth int
	var sampleRate int
	var channels int
	var sizeBytes int64
	var modifiedAt sql.NullString

	query := `
		SELECT embedded_tags_json, checksum, relative_path, file_name, container, mime_type, codec,
		       codec_profile, metadata_formats_json, channel_layout, duration_seconds, bitrate,
		       bit_depth, sample_rate, channels, size_bytes, modified_at
		FROM media_files
		WHERE library_id = ? AND path = ? AND ` + ownerColumn + ` IS NOT NULL AND ` + ownerColumn + ` != ''`
	err := s.db.QueryRowContext(ctx, query, libraryID, path).Scan(
		&embeddedJSON, &checksum, &relativePath, &fileName, &container, &mimeType, &codec,
		&codecProfile, &metadataFormatsJSON, &channelLayout, &durationSeconds, &bitrate,
		&bitDepth, &sampleRate, &channels, &sizeBytes, &modifiedAt,
	)
	if err != nil {
		return probeInfo{}, err
	}

	tags := catalog.Tags{}
	if embeddedJSON != "" {
		_ = json.Unmarshal([]byte(embeddedJSON), &tags)
	}

	stat, statErr := os.Stat(path)
	var modified *time.Time
	if statErr == nil {
		value := stat.ModTime().UTC()
		modified = &value
		if sizeBytes == 0 {
			sizeBytes = stat.Size()
		}
	} else if modifiedAt.Valid {
		if parsed, parseErr := time.Parse(time.RFC3339, modifiedAt.String); parseErr == nil {
			value := parsed.UTC()
			modified = &value
		}
	}

	if checksum == "" {
		checksum = fileChecksum(path, stat)
	}
	if fileName == "" {
		fileName = filepath.Base(path)
	}

	var metadataFormats []string
	if metadataFormatsJSON != "" {
		_ = json.Unmarshal([]byte(metadataFormatsJSON), &metadataFormats)
	}
	if len(metadataFormats) == 0 {
		metadataFormats = metadataFormatsForPath(path, tags)
	}

	audioFile := catalog.AudioFile{
		Path:            path,
		RelativePath:    relativePath,
		FileName:        fileName,
		Container:       container,
		MimeType:        mimeType,
		Codec:           codec,
		CodecProfile:    codecProfile,
		MetadataFormats: metadataFormats,
		Bitrate:         bitrate,
		BitDepth:        bitDepth,
		SampleRate:      sampleRate,
		Channels:        channels,
		ChannelLayout:   channelLayout,
		DurationSeconds: durationSeconds,
		SizeBytes:       sizeBytes,
		ModifiedAt:      modified,
		Checksum:        checksum,
		EmbeddedTags:    tags,
	}

	return probeInfo{
		AudioFile: audioFile,
		Tags:      tags,
	}, nil
}
