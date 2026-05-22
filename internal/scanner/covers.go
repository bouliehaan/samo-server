package scanner

import (
	"context"
	"path/filepath"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type CoverResolver interface {
	ResolveForAudio(ctx context.Context, audioPath, sourceChecksum string) (*catalog.Image, error)
}

func (s *Scanner) resolveCover(ctx context.Context, dir string, audioPaths []string, checksums []string) *catalog.Image {
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
		cover, err := s.covers.ResolveForAudio(ctx, audioPath, checksum)
		if err == nil && cover != nil {
			return cover
		}
	}
	return nil
}

func (s *Scanner) firstAudioCover(ctx context.Context, path string, file catalog.AudioFile) *catalog.Image {
	return s.resolveCover(ctx, filepath.Dir(path), []string{path}, []string{file.Checksum})
}
