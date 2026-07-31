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

	cached, err := s.store.CachedProbeForOwner(ctx, libraryID, path, ownerColumn)
	if err != nil {
		return probeInfo{}, err
	}
	embeddedJSON := cached.EmbeddedTagsJSON
	checksum := cached.Checksum
	fileName := cached.FileName
	sizeBytes := cached.SizeBytes
	modifiedAt := cached.ModifiedAt

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
	if cached.MetadataFormatsJSON != "" {
		_ = json.Unmarshal([]byte(cached.MetadataFormatsJSON), &metadataFormats)
	}
	if len(metadataFormats) == 0 {
		metadataFormats = metadataFormatsForPath(path, tags)
	}

	audioFile := catalog.AudioFile{
		Path:            path,
		RelativePath:    cached.RelativePath,
		FileName:        fileName,
		Container:       cached.Container,
		MimeType:        cached.MimeType,
		Codec:           cached.Codec,
		CodecProfile:    cached.CodecProfile,
		MetadataFormats: metadataFormats,
		Bitrate:         cached.Bitrate,
		BitDepth:        cached.BitDepth,
		SampleRate:      cached.SampleRate,
		Channels:        cached.Channels,
		ChannelLayout:   cached.ChannelLayout,
		DurationSeconds: cached.DurationSeconds,
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
