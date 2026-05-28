package scanner

import (
	"path/filepath"
	"strings"
)

// LibraryKindFromPath guesses the intended library kind from the folder
// name when users mount a dedicated tree like /mnt/media/Podcasts.
func LibraryKindFromPath(path string) string {
	base := strings.ToLower(filepath.Base(filepath.Clean(strings.TrimSpace(path))))
	switch {
	case strings.Contains(base, "podcast"):
		return "podcast"
	case strings.Contains(base, "audiobook"), strings.Contains(base, "audiobooks"), base == "books":
		return "audiobook"
	case strings.Contains(base, "music"):
		return "music"
	default:
		return ""
	}
}

func libraryRootLooksLikePodcast(path string) bool {
	return LibraryKindFromPath(path) == "podcast"
}
