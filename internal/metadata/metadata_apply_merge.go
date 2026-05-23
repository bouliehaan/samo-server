package metadata

import (
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func mergeExternalIDs(current, candidate catalog.ExternalIDs) catalog.ExternalIDs {
	merged := current
	if candidate.MusicBrainzArtistID != "" {
		merged.MusicBrainzArtistID = candidate.MusicBrainzArtistID
	}
	if candidate.MusicBrainzReleaseGroupID != "" {
		merged.MusicBrainzReleaseGroupID = candidate.MusicBrainzReleaseGroupID
	}
	if candidate.MusicBrainzReleaseID != "" {
		merged.MusicBrainzReleaseID = candidate.MusicBrainzReleaseID
	}
	if candidate.MusicBrainzRecordingID != "" {
		merged.MusicBrainzRecordingID = candidate.MusicBrainzRecordingID
	}
	if candidate.MusicBrainzTrackID != "" {
		merged.MusicBrainzTrackID = candidate.MusicBrainzTrackID
	}
	if candidate.MusicBrainzWorkID != "" {
		merged.MusicBrainzWorkID = candidate.MusicBrainzWorkID
	}
	if candidate.DiscogsID != "" {
		merged.DiscogsID = candidate.DiscogsID
	}
	if candidate.SpotifyID != "" {
		merged.SpotifyID = candidate.SpotifyID
	}
	if candidate.AppleMusicID != "" {
		merged.AppleMusicID = candidate.AppleMusicID
	}
	if candidate.ISRC != "" {
		merged.ISRC = candidate.ISRC
	}
	if candidate.ISBN10 != "" {
		merged.ISBN10 = candidate.ISBN10
	}
	if candidate.ISBN13 != "" {
		merged.ISBN13 = candidate.ISBN13
	}
	if candidate.ASIN != "" {
		merged.ASIN = candidate.ASIN
	}
	if candidate.AudibleASIN != "" {
		merged.AudibleASIN = candidate.AudibleASIN
	}
	if candidate.GoogleBooksID != "" {
		merged.GoogleBooksID = candidate.GoogleBooksID
	}
	if candidate.OpenLibraryID != "" {
		merged.OpenLibraryID = candidate.OpenLibraryID
	}
	if candidate.ITunesID != "" {
		merged.ITunesID = candidate.ITunesID
	}
	if candidate.FeedGUID != "" {
		merged.FeedGUID = candidate.FeedGUID
	}
	merged.URLs = mergeStringSlices(current.URLs, candidate.URLs)
	return merged
}

func mergeStringSlices(current, incoming []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(current)+len(incoming))
	for _, value := range append(current, incoming...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func candidateHasValue(candidate SearchResult, field string) bool {
	switch field {
	case "title", "name":
		return strings.TrimSpace(candidate.Title) != ""
	case "sortTitle", "sortName":
		return strings.TrimSpace(candidate.SortTitle) != ""
	case "subtitle":
		return strings.TrimSpace(candidate.Subtitle) != ""
	case "description":
		return strings.TrimSpace(candidate.Description) != ""
	case "publisher":
		return strings.TrimSpace(candidate.Publisher) != ""
	case "publishedDate":
		return strings.TrimSpace(candidate.PublishedDate) != ""
	case "publishedYear":
		return strings.TrimSpace(candidate.PublishedYear) != ""
	case "language":
		return strings.TrimSpace(candidate.Language) != ""
	case "author", "displayArtist":
		return len(candidate.Authors) > 0 || strings.TrimSpace(firstContributorName(candidate.Authors)) != ""
	case "siteUrl":
		return firstLinkURL(candidate.Links) != ""
	case "imageUrl", "cover":
		return candidate.Cover != nil && strings.TrimSpace(candidate.Cover.URL) != ""
	case "genres", "categories", "styles", "moods":
		return len(candidate.Genres) > 0
	case "tags":
		return len(candidate.Tags) > 0
	case "authors", "narrators", "artists":
		return len(candidate.Authors) > 0 || len(candidate.Narrators) > 0
	case "series":
		return len(candidate.Series) > 0
	case "externalIds":
		return !externalIDsEmpty(candidate.ExternalIDs)
	case "explicit", "abridged":
		return true
	case "releaseDate", "originalReleaseDate", "releaseYear", "releaseType", "version",
		"recordLabel", "catalogNumber", "barcode":
		return strings.TrimSpace(candidate.Title) != "" ||
			strings.TrimSpace(candidate.Publisher) != "" ||
			strings.TrimSpace(candidate.PublishedDate) != "" ||
			strings.TrimSpace(candidate.PublishedYear) != ""
	default:
		return false
	}
}

func firstContributorName(contributors []catalog.Contributor) string {
	if len(contributors) == 0 {
		return ""
	}
	return strings.TrimSpace(contributors[0].Name)
}

func firstLinkURL(links []Link) string {
	for _, link := range links {
		if url := strings.TrimSpace(link.URL); url != "" {
			return url
		}
	}
	return ""
}

func externalIDsEmpty(ids catalog.ExternalIDs) bool {
	return ids.MusicBrainzArtistID == "" &&
		ids.MusicBrainzReleaseGroupID == "" &&
		ids.MusicBrainzReleaseID == "" &&
		ids.MusicBrainzRecordingID == "" &&
		ids.MusicBrainzTrackID == "" &&
		ids.MusicBrainzWorkID == "" &&
		ids.DiscogsID == "" &&
		ids.SpotifyID == "" &&
		ids.AppleMusicID == "" &&
		ids.ISRC == "" &&
		ids.ISBN10 == "" &&
		ids.ISBN13 == "" &&
		ids.ASIN == "" &&
		ids.AudibleASIN == "" &&
		ids.GoogleBooksID == "" &&
		ids.OpenLibraryID == "" &&
		ids.ITunesID == "" &&
		ids.FeedGUID == "" &&
		len(ids.URLs) == 0
}

func coverFromCandidate(candidate SearchResult) *catalog.Image {
	if candidate.Cover == nil {
		return nil
	}
	url := strings.TrimSpace(candidate.Cover.URL)
	if url == "" {
		return nil
	}
	image := *candidate.Cover
	if image.ID == "" {
		image.ID = stableApplyID("cover", url)
	}
	image.URL = url
	return &image
}

func isbnsFromCandidate(candidate SearchResult) []string {
	var values []string
	if candidate.ExternalIDs.ISBN13 != "" {
		values = append(values, candidate.ExternalIDs.ISBN13)
	}
	if candidate.ExternalIDs.ISBN10 != "" {
		values = append(values, candidate.ExternalIDs.ISBN10)
	}
	return mergeStringSlices(nil, values)
}
