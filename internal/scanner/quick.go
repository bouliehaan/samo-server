package scanner

import (
	"context"
	"strings"

	"github.com/bouliehaan/samo-server/internal/scannerstore"
)

const (
	ScanModeFull   = "full"
	ScanModeQuick  = "quick"
	ScanModeRepair = "repair"
)

// indexedFile is what a quick scan compares a file on disk against.
type indexedFile = scannerstore.IndexedFile

func (s *Scanner) loadFileIndex(ctx context.Context, libraryID string) (map[string]indexedFile, error) {
	return s.store.FileIndex(ctx, libraryID)
}

func (s *Scanner) fileNeedsProbe(path string) bool {
	if s.scanMode != ScanModeQuick || s.fileIndex == nil {
		return true
	}
	entry, ok := s.fileIndex[path]
	if !ok {
		return true
	}
	stat, err := statWithTimeout(path, pathStatTimeout)
	if err != nil {
		return true
	}
	return fileChecksum(path, stat) != entry.Checksum
}

func (s *Scanner) markIndexedFileSeen(path string) bool {
	if s.activeScan == nil || s.fileIndex == nil {
		return false
	}
	entry, ok := s.fileIndex[path]
	if !ok {
		return false
	}
	s.activeScan.seeFile(path)
	if entry.AudiobookID != "" {
		s.activeScan.seeAudiobook(entry.AudiobookID)
	}
	if entry.PodcastID != "" {
		s.activeScan.seePodcast(entry.PodcastID)
	}
	if entry.EpisodeID != "" {
		s.activeScan.seeEpisode(entry.EpisodeID)
	}
	return true
}

func (s *Scanner) skipUnchangedFile(path string) bool {
	if s.scanMode == ScanModeRepair {
		return false
	}
	if s.scanMode != ScanModeQuick {
		return false
	}
	if s.fileNeedsProbe(path) {
		return false
	}
	return s.markIndexedFileSeen(path)
}

func (s *Scanner) groupNeedsProbe(files []string) bool {
	if s.scanMode != ScanModeQuick {
		return true
	}
	for _, path := range files {
		if s.fileNeedsProbe(path) {
			return true
		}
	}
	return false
}

func (s *Scanner) markIndexedGroupSeen(files []string) {
	for _, path := range files {
		if !s.fileNeedsProbe(path) {
			s.markIndexedFileSeen(path)
		}
	}
}

// markAudiobookGroupSeen records an audiobook and its files during quick scans
// that skip ffprobe. Without this, prune deletes the whole audiobook library.
func (s *Scanner) markAudiobookGroupSeen(libraryID string, group groupedAudio) {
	if s.activeScan == nil {
		return
	}
	s.activeScan.seeAudiobook(stableID("audiobook", libraryID, group.Root))
	for _, path := range group.Files {
		if !s.markIndexedFileSeen(path) {
			s.activeScan.seeFile(path)
		}
	}
}

// markPodcastGroupSeen is the podcast equivalent of markAudiobookGroupSeen.
func (s *Scanner) markPodcastGroupSeen(libraryID string, group groupedAudio) {
	if s.activeScan == nil {
		return
	}
	s.activeScan.seePodcast(stableID("podcast", libraryID, group.Root))
	for _, path := range group.Files {
		if !s.markIndexedFileSeen(path) {
			s.activeScan.seeFile(path)
		}
	}
}

func normalizeScanMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ScanModeQuick:
		return ScanModeQuick
	default:
		return ScanModeFull
	}
}
