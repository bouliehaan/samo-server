package scanner

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// indexedMediaFile is the subset of media_files columns used for move detection.
type indexedMediaFile struct {
	ID          string
	LibraryID   string
	Path        string
	TrackID     string
	TrackPID    string
	ContentHash string
	AlbumID     string
	Missing     bool
}

// contentHashFromProbe builds a Navidrome-style metadata fingerprint used to
// detect exact matches between a missing file and a newly scanned one.
func contentHashFromProbe(libraryID, relPath string, tags catalog.Tags, albumID string, probe probeInfo) string {
	parts := []string{
		libraryID,
		albumID,
		firstTag(tags, "title"),
		firstTag(tags, "album"),
		firstTag(tags, "album_artist", "albumartist"),
		firstTag(tags, "artist"),
		firstTag(tags, "discnumber", "disc"),
		firstTag(tags, "tracknumber", "track"),
		strings.TrimSpace(relPath),
	}
	if probe.AudioFile.DurationSeconds > 0 {
		parts = append(parts, strconv.Itoa(probe.AudioFile.DurationSeconds))
	}
	if probe.AudioFile.SizeBytes > 0 {
		parts = append(parts, strconv.FormatInt(probe.AudioFile.SizeBytes, 10))
	}
	return stableID("mfhash", parts...)
}

func mediaFileEquals(a, b indexedMediaFile) bool {
	if a.ContentHash == "" || b.ContentHash == "" {
		return false
	}
	return a.ContentHash == b.ContentHash
}

func mediaFileEquivalent(a, b indexedMediaFile) bool {
	return strings.EqualFold(filepath.Base(a.Path), filepath.Base(b.Path))
}
