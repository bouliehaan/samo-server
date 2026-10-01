// Package musicrelease defines which releases can name a song's own record.
package musicrelease

import (
	"regexp"
	"strings"
)

// These patterns catch playlist exports whose provider omitted Compilation.
// Keep them narrow: words such as "Hits", "Collection", and "Now" can also
// be legitimate album titles.
var compilationTitle = regexp.MustCompile(`(?i)\bnow[ ’']*that[ ’']*s\s+what\s+i\s+call\s+music\b|\btop\s*[-:]?\s*\d{2,4}\b|\b(?:deezer|spotify)\s+(?:charts?|hits|playlist)\b`)

func CompilationTitle(title string) bool {
	return compilationTitle.MatchString(title)
}

func Derived(types []string) bool {
	for _, kind := range types {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "compilation", "live", "remix", "dj-mix", "soundtrack":
			return true
		}
	}
	return false
}

// Rejected releases cannot supply album metadata, even as a last resort.
func Rejected(title, primary string, secondary []string) bool {
	return CompilationTitle(title) || Derived(secondary) || Derived([]string{primary})
}

func Rank(primary string) int {
	switch strings.ToLower(strings.TrimSpace(primary)) {
	case "album":
		return 0
	case "single":
		return 1
	case "ep":
		return 2
	default:
		return 3
	}
}
