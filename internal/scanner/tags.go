package scanner

import (
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// tagAliases maps canonical lookup keys to normalized ffprobe tag names.
var tagAliases = map[string][]string{
	"title": {
		"title", "tit2", "tracktitle", "track_title", "name",
	},
	"album": {
		"album", "talb", "albumtitle", "album_title",
	},
	"artist": {
		"artist", "artists", "tpe1", "performer", "performer_name",
	},
	"album_artist": {
		"album_artist", "albumartist", "albumartists", "album_artists", "tpe2", "aart", "band",
	},
	"album_artist_display": {
		"albumartists", "album_artists", "album_artist_display",
	},
	"artist_display": {
		"artists", "artist_display",
	},
	"author": {
		"author", "authors", "writer", "book_author",
	},
	"narrator": {
		"narrator", "narrators", "read_by", "readby", "nrt",
	},
	"genre": {
		"genre", "genres", "tcon",
	},
	"date": {
		"date", "year", "tdrc", "tyer", "releasedate", "release_date", "recordingdate",
	},
	"originaldate": {
		"originaldate", "originalyear", "original_date", "original_year", "tdor",
	},
	"discnumber": {
		"discnumber", "disc", "disk", "disknumber", "tpos", "part", "disc_number",
	},
	"tracknumber": {
		"tracknumber", "track", "trck", "track_number", "tracknum",
	},
	"totaldiscs": {
		"totaldiscs", "totaldisc", "total_discs", "discs", "set", "tpos",
	},
	"totaltracks": {
		"totaltracks", "total_tracks", "tracktotal",
	},
	"compilation": {
		"compilation", "itunescompilation", "tcmp", "cpil",
	},
	"explicit": {
		"explicit", "itunesadvisory", "advisory", "rating",
	},
	"isrc": {
		"isrc", "tsrc",
	},
	"barcode": {
		"barcode", "upc", "ean", "gtin", "barcode_number",
	},
	"lyrics": {
		"lyrics", "unsyncedlyrics", "unsynced_lyrics", "lyr",
	},
	"comment": {
		"comment", "description", "comm", "summary",
	},
	"subtitle": {
		"subtitle", "stik",
	},
	"language": {
		"language", "lang", "tlang",
	},
	"series": {
		"series", "series_name", "grouping", "content_group", "movementname",
	},
	"series_part": {
		"series_part", "series_sequence", "series_index", "group_position", "part",
	},
	"publisher": {
		"publisher", "organization", "label", "pub",
	},
	"podcast": {
		"podcast", "show", "podcast_title",
	},
	"feed_url": {
		"podcasturl", "feed", "feed_url", "rss", "podcast_url",
	},
	"guid": {
		"guid", "episode_guid", "podcast_guid",
	},
	"season": {
		"season", "season_number",
	},
	"episode": {
		"episode", "episode_number", "episodenumber",
	},
	"movementname": {
		"movementname", "movement", "chapter",
	},
}

func tagKeysFor(keys ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(keys)*3)
	for _, key := range keys {
		normalized := normalizeTagKey(key)
		candidates := []string{normalized}
		if aliases, ok := tagAliases[normalized]; ok {
			candidates = aliases
		}
		for _, candidate := range candidates {
			candidate = normalizeTagKey(candidate)
			if _, exists := seen[candidate]; exists {
				continue
			}
			seen[candidate] = struct{}{}
			out = append(out, candidate)
		}
	}
	return out
}

func firstTag(tags catalog.Tags, keys ...string) string {
	for _, key := range tagKeysFor(keys...) {
		values := tags[key]
		if len(values) > 0 && strings.TrimSpace(values[0]) != "" {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

func splitTag(tags catalog.Tags, keys ...string) []string {
	value := firstTag(tags, keys...)
	if value == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ';' || r == '|' || r == '\\'
	})
	return cleanParts(parts)
}

func splitGenreTag(tags catalog.Tags, keys ...string) []string {
	value := firstTag(tags, keys...)
	if value == "" {
		return nil
	}
	value = strings.ReplaceAll(value, "//", "/")
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ';' || r == '|' || r == '/' || r == '\\'
	})
	return cleanParts(parts)
}

func boolTag(tags catalog.Tags, keys ...string) bool {
	value := strings.ToLower(firstTag(tags, keys...))
	return value == "1" || value == "true" || value == "yes" || value == "y"
}

func explicitTag(tags catalog.Tags) bool {
	advisory := strings.ToLower(firstTag(tags, "itunesadvisory", "advisory", "rating"))
	switch advisory {
	case "1", "explicit", "4":
		return true
	case "2", "clean":
		return false
	}
	return boolTag(tags, "explicit")
}

func mergeProbeTags(probes []probedFile) catalog.Tags {
	merged := catalog.Tags{}
	for _, probe := range probes {
		for key, values := range probe.Tags {
			if len(values) == 0 {
				continue
			}
			if len(merged[key]) == 0 {
				merged[key] = append([]string(nil), values...)
			}
		}
	}
	return merged
}

func barcodeFromTags(tags catalog.Tags) string {
	return firstTag(tags, "barcode", "upc", "ean", "gtin")
}

func parseDatePtr(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"2006",
		"02-01-2006",
		"01/02/2006",
	}
	for _, format := range formats {
		parsed, err := time.Parse(format, value)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	if len(value) >= 4 {
		if year, err := strconv.Atoi(value[:4]); err == nil && year > 0 {
			parsed := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
			return &parsed
		}
	}
	return nil
}
