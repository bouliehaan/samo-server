package sources

import (
	"regexp"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type existingPodcastEpisode struct {
	ID              string
	Title           string
	PublishedAt     *time.Time
	Season          int
	Episode         int
	ExternalIDs     catalog.ExternalIDs
	HasLocalFile    bool
	EnclosureURL    string
	DurationSeconds int
}

var nonAlnumTitle = regexp.MustCompile(`[^a-z0-9]+`)

func normalizeEpisodeTitle(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	return nonAlnumTitle.ReplaceAllString(value, " ")
}

func externalIDTokens(ids catalog.ExternalIDs) []string {
	tokens := make([]string, 0, 4+len(ids.URLs))
	if guid := strings.TrimSpace(ids.FeedGUID); guid != "" {
		tokens = append(tokens, strings.ToLower(guid))
	}
	if itunes := strings.TrimSpace(ids.ITunesID); itunes != "" {
		tokens = append(tokens, strings.ToLower(itunes))
	}
	for _, url := range ids.URLs {
		if trimmed := strings.TrimSpace(url); trimmed != "" {
			tokens = append(tokens, strings.ToLower(trimmed))
		}
	}
	return tokens
}

func rssEpisodeTokens(episode parsedPodcastEpisode) []string {
	tokens := externalIDTokens(catalog.ExternalIDs{
		FeedGUID: episode.GUID,
		URLs:     append([]string{episode.Link, episode.EnclosureURL}, episode.ExternalURLs...),
	})
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func sharesExternalToken(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	set := map[string]struct{}{}
	for _, token := range left {
		set[token] = struct{}{}
	}
	for _, token := range right {
		if _, ok := set[token]; ok {
			return true
		}
	}
	return false
}

func publishedWithinTolerance(left, right *time.Time, tolerance time.Duration) bool {
	if left == nil || right == nil {
		return true
	}
	delta := left.Sub(*right)
	if delta < 0 {
		delta = -delta
	}
	return delta <= tolerance
}

func findMatchingEpisode(
	parsed parsedPodcastEpisode,
	existing []existingPodcastEpisode,
	used map[string]struct{},
) *existingPodcastEpisode {
	rssTokens := rssEpisodeTokens(parsed)
	normalizedTitle := normalizeEpisodeTitle(parsed.Title)

	var titleDateCandidate *existingPodcastEpisode
	var titleOnlyCandidate *existingPodcastEpisode

	for index := range existing {
		candidate := &existing[index]
		if _, ok := used[candidate.ID]; ok {
			continue
		}

		if sharesExternalToken(rssTokens, externalIDTokens(candidate.ExternalIDs)) {
			return candidate
		}

		if parsed.Season > 0 && parsed.Episode > 0 &&
			candidate.Season == parsed.Season && candidate.Episode == parsed.Episode {
			return candidate
		}

		if normalizedTitle == "" || normalizeEpisodeTitle(candidate.Title) != normalizedTitle {
			continue
		}

		if publishedWithinTolerance(candidate.PublishedAt, parsed.PublishedAt, 7*24*time.Hour) {
			if titleDateCandidate == nil {
				titleDateCandidate = candidate
			}
			continue
		}
		if titleOnlyCandidate == nil {
			titleOnlyCandidate = candidate
		}
	}

	if titleDateCandidate != nil {
		return titleDateCandidate
	}
	return titleOnlyCandidate
}

func mergeRSSIntoExisting(
	existing existingPodcastEpisode,
	parsed parsedPodcastEpisode,
	podcastID string,
	libraryID string,
) catalog.PodcastEpisode {
	externalIDs := existing.ExternalIDs
	externalIDs.FeedGUID = firstNonEmpty(parsed.GUID, externalIDs.FeedGUID)
	externalIDs.URLs = uniqueEpisodeStrings(append(
		append([]string{}, externalIDs.URLs...),
		append([]string{parsed.Link, parsed.EnclosureURL}, parsed.ExternalURLs...)...,
	))

	publishedAt := existing.PublishedAt
	if parsed.PublishedAt != nil {
		publishedAt = parsed.PublishedAt
	}

	durationSeconds := existing.DurationSeconds
	if durationSeconds <= 0 && parsed.DurationSeconds > 0 {
		durationSeconds = parsed.DurationSeconds
	}

	title := strings.TrimSpace(existing.Title)
	if title == "" {
		title = parsed.Title
	}

	description := ""
	subtitle := parsed.Subtitle

	return catalog.PodcastEpisode{
		ID:              existing.ID,
		LibraryID:       libraryID,
		PodcastID:       podcastID,
		Title:           title,
		Subtitle:        subtitle,
		Description:     firstNonEmpty(parsed.Description, description),
		PublishedAt:     publishedAt,
		Season:          firstNonZero(parsed.Season, existing.Season),
		Episode:         firstNonZero(parsed.Episode, existing.Episode),
		EpisodeType:     parsed.EpisodeType,
		DurationSeconds: durationSeconds,
		Explicit:        parsed.Explicit || existing.HasLocalFile,
		EnclosureURL:    parsed.EnclosureURL,
		EnclosureType:   parsed.EnclosureType,
		EnclosureBytes:  parsed.EnclosureBytes,
		ExternalIDs:     externalIDs,
	}
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func uniqueEpisodeStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
