package scanner

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const (
	audiobookChapterProbeCap = 512 << 20
)

func audiobookChapterProbeLimits(path string) (probeSize, analyzeDuration string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 {
		return "128M", "50M"
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".m4b" && ext != ".m4a" {
		return "32M", "10M"
	}
	size := info.Size()
	if size > audiobookChapterProbeCap {
		return "512M", "200M"
	}
	return fmt.Sprintf("%d", size), fmt.Sprintf("%d", min64(size, 200<<20))
}

func (s *Scanner) probeAudiobookChapterMarkers(ctx context.Context, path string) []catalog.AudioChapter {
	probeSize, analyzeDuration := audiobookChapterProbeLimits(path)
	ff, err := s.probeMediaFFprobeWithTimeout(ctx, path, true, probeSize, analyzeDuration, probeFileTimeout)
	if err == nil && len(ff.Chapters) > 0 {
		return ff.Chapters
	}
	if err != nil {
		log.Printf("scanner: ffprobe chapters for %q: %v", path, err)
	}

	if chapters, chplErr := mp4ChaptersFromFile(path); chplErr == nil && len(chapters) > 0 {
		return chapters
	}
	return nil
}
