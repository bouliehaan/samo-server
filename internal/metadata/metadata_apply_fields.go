package metadata

import "strings"

// allowedFieldsForTarget returns the apply-fields whitelist for each
// target kind. There is no longer a `mediaType` parameter — audiobooks
// and podcasts are different ApplyTargetKinds, so the right field list
// is selected unambiguously.
func allowedFieldsForTarget(kind ApplyTargetKind) []string {
	switch kind {
	case ApplyTargetAudiobook:
		return []string{
			"title", "subtitle", "sortTitle", "description", "publisher", "publishedDate", "publishedYear",
			"language", "genres", "tags", "explicit", "abridged", "authors", "narrators", "series",
			"cover", "externalIds",
		}
	case ApplyTargetPodcast:
		return []string{
			"title", "description", "author", "siteUrl", "language", "genres", "categories",
			"explicit", "cover", "externalIds",
		}
	case ApplyTargetPodcastEpisode:
		return []string{
			"title", "subtitle", "description", "publishedAt", "explicit", "externalIds",
		}
	case ApplyTargetMusicArtist:
		return []string{"name", "sortName", "description", "genres", "tags", "externalIds"}
	case ApplyTargetMusicAlbum:
		return []string{
			"title", "sortTitle", "version", "displayArtist", "releaseDate", "originalReleaseDate",
			"releaseYear", "releaseType", "recordLabel", "catalogNumber", "barcode", "genres", "styles",
			"moods", "tags", "cover", "externalIds", "artists",
		}
	case ApplyTargetMusicTrack:
		return []string{
			"title", "sortTitle", "subtitle", "displayArtist", "releaseDate", "releaseYear", "genres",
			"moods", "tags", "explicit", "cover", "externalIds", "artists",
		}
	case ApplyTargetPodcastFeed:
		return []string{
			"title", "description", "author", "siteUrl", "imageUrl", "language", "categories",
			"explicit", "externalIds",
		}
	default:
		return nil
	}
}

func normalizeApplyFields(fields []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

func validateApplyFields(kind ApplyTargetKind, fields []string) ([]string, error) {
	fields = normalizeApplyFields(fields)
	allowed := allowedFieldsForTarget(kind)
	if len(allowed) == 0 {
		return nil, ErrInvalidApplyTarget
	}
	if len(fields) == 0 {
		return append([]string(nil), allowed...), nil
	}
	allowedSet := map[string]struct{}{}
	for _, field := range allowed {
		allowedSet[field] = struct{}{}
	}
	for _, field := range fields {
		if _, ok := allowedSet[field]; !ok {
			return nil, ErrInvalidApplyField
		}
	}
	return fields, nil
}

func fieldSet(fields []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, field := range fields {
		set[field] = struct{}{}
	}
	return set
}

func wantsField(set map[string]struct{}, name string) bool {
	_, ok := set[name]
	return ok
}
