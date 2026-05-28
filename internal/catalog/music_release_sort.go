package catalog

import (
	"strconv"
	"strings"
	"time"
)

// albumReleaseSortKey returns a comparable release instant for sorting.
// Unknown releases map to the zero time and sort after dated albums when descending.
func albumReleaseSortKey(album MusicAlbum) time.Time {
	if t := parseReleaseDateString(album.ReleaseDate); !t.IsZero() {
		return t
	}
	if t := parseReleaseDateString(album.OriginalReleaseDate); !t.IsZero() {
		return t
	}
	if album.ReleaseYear > 0 {
		return time.Date(album.ReleaseYear, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Time{}
}

func parseReleaseDateString(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if year, err := strconv.Atoi(raw); err == nil && year > 0 {
		return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
		"2006",
		"01/02/2006",
		"02/01/2006",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func compareReleaseSortKeys(left, right time.Time, desc bool) int {
	if left.IsZero() && right.IsZero() {
		return 0
	}
	if left.IsZero() {
		return 1
	}
	if right.IsZero() {
		return -1
	}
	if left.Equal(right) {
		return 0
	}
	if desc {
		if left.After(right) {
			return -1
		}
		return 1
	}
	if left.Before(right) {
		return -1
	}
	return 1
}
