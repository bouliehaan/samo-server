package scanner

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

var firstNumberPattern = regexp.MustCompile(`\d+`)

func mediaOrder(tags catalog.Tags, path string) (int, int) {
	disc, _ := parseNumberPair(firstTag(tags, "discnumber", "disc", "disk", "tpos"))
	track, _ := parseNumberPair(firstTag(tags, "tracknumber", "track", "trk", "trck"))
	if disc == 0 {
		disc = filenameDiscNumber(path)
	}
	if track == 0 {
		track = filenameTrackNumber(path)
	}
	return disc, track
}

func filenameTrackNumber(path string) int {
	match := firstNumberPattern.FindString(filepath.Base(path))
	if match == "" {
		return 0
	}
	return int(parseInt64(match))
}

func filenameDiscNumber(path string) int {
	matches := firstNumberPattern.FindAllString(filepath.Base(path), 2)
	if len(matches) < 2 {
		return 0
	}
	name := filepath.Base(path)
	if containsFold(name, "disc") || containsFold(name, "disk") || containsFold(name, "cd") {
		return int(parseInt64(matches[0]))
	}
	return 0
}

func containsFold(value string, needle string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(needle))
}
