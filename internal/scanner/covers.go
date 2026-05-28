package scanner

import (
	"context"
	"path/filepath"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type CoverResolver interface {
	// embeddedKnown is set when the caller already probed the file: true if an
	// attached picture stream exists, false if not. When nil, ResolveForAudio
	// runs a lightweight ffprobe to check before extracting.
	ResolveForAudio(ctx context.Context, audioPath, sourceChecksum string, embeddedKnown *bool) (*catalog.Image, error)
	// LookupCached returns a previously extracted cover without running ffmpeg.
	LookupCached(ctx context.Context, audioPath, sourceChecksum string) (*catalog.Image, error)
}

func (s *Scanner) resolveCover(
	ctx context.Context,
	dir string,
	audioPaths []string,
	checksums []string,
	embeddedKnown *bool,
) *catalog.Image {
	if cover := findCoverImage(dir); cover != nil {
		return cover
	}
	if s.covers == nil {
		return nil
	}
	for index, audioPath := range audioPaths {
		checksum := ""
		if index < len(checksums) {
			checksum = checksums[index]
		}
		var cover *catalog.Image
		var err error
		cover, err = s.covers.LookupCached(ctx, audioPath, checksum)
		if err == nil && cover != nil {
			return cover
		}
		// Full scans prefer cache hits for speed, but still extract embedded art
		// when ffprobe reported a picture stream and nothing is cached yet.
		tryExtract := s.scanMode != ScanModeFull
		if !tryExtract && embeddedKnown != nil && *embeddedKnown {
			tryExtract = true
		}
		if tryExtract {
			cover, err = s.covers.ResolveForAudio(ctx, audioPath, checksum, embeddedKnown)
			if err == nil && cover != nil {
				return cover
			}
		}
	}
	return nil
}

func (s *Scanner) firstAudioCover(ctx context.Context, path string, file catalog.AudioFile, embeddedKnown *bool) *catalog.Image {
	return s.resolveCover(ctx, filepath.Dir(path), []string{path}, []string{file.Checksum}, embeddedKnown)
}
