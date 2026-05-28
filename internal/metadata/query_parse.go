package metadata

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	audiobookNoisePattern = regexp.MustCompile(`(?i)\b(unabridged|abridged|audiobook|audible)\b`)
	bracketContentPattern = regexp.MustCompile(`[\[\(][^\]\)]*[\]\)]`)
	trackPrefixPattern    = regexp.MustCompile(`(?i)^\s*\d{1,3}\s*[-.:)\]]\s*`)
)

// prepareSearchRequest turns messy client input into structured title/author
// fields and clears the free-text query when structured fields are available.
// Providers like Open Library and Google Books match much better on separate
// title + author params than on one long concatenated string.
func prepareSearchRequest(request SearchRequest) SearchRequest {
	switch request.Kind {
	case KindAudiobook:
		return prepareAudiobookSearchRequest(request)
	case KindPodcast:
		return preparePodcastSearchRequest(request)
	default:
		return request
	}
}

func prepareAudiobookSearchRequest(request SearchRequest) SearchRequest {
	title := strings.TrimSpace(request.Title)
	author := strings.TrimSpace(request.Author)
	query := strings.TrimSpace(request.Query)

	if title == "" && author == "" && query != "" {
		title, author = splitTitleAuthorQuery(query)
	}
	if author == "" && title != "" {
		if inferredTitle, inferredAuthor := inferTitleAuthorFromFreeText(title); inferredAuthor != "" {
			title = inferredTitle
			author = inferredAuthor
		}
	}
	title = cleanAudiobookTitle(title)
	author = cleanPersonName(author)

	request.Title = title
	request.Author = author
	if title != "" || author != "" {
		request.Query = ""
	}
	return request
}

func preparePodcastSearchRequest(request SearchRequest) SearchRequest {
	title := strings.TrimSpace(firstNonEmpty(request.Title, request.Query))
	title = cleanPodcastTitle(title)
	request.Title = title
	request.Query = ""
	return request
}

// searchAttempts returns ordered provider queries to try when the first pass
// returns nothing useful. Each attempt is cheap relative to a failed match.
func searchAttempts(request SearchRequest) []SearchRequest {
	base := prepareSearchRequest(request)
	attempts := []SearchRequest{base}

	switch base.Kind {
	case KindAudiobook:
		if base.Title != "" && base.Author != "" {
			attempts = append(attempts, cloneSearchRequest(base, base.Title, "", ""))
			if surname := authorSurname(base.Author); surname != "" && !strings.EqualFold(surname, base.Author) {
				attempts = append(attempts, cloneSearchRequest(base, base.Title, surname, ""))
			}
		}
		if base.Title != "" && base.Author == "" {
			if inferredTitle, inferredAuthor := inferTitleAuthorFromFreeText(base.Title); inferredAuthor != "" {
				attempts = append(attempts, cloneSearchRequest(base, inferredTitle, inferredAuthor, ""))
				attempts = append(attempts, cloneSearchRequest(base, inferredTitle, "", ""))
				if surname := authorSurname(inferredAuthor); surname != "" {
					attempts = append(attempts, cloneSearchRequest(base, inferredTitle, surname, ""))
				}
			}
		}
		if core := coreTitle(base.Title); core != "" && !strings.EqualFold(core, base.Title) {
			attempts = append(attempts, cloneSearchRequest(base, core, base.Author, ""))
			if base.Author != "" {
				attempts = append(attempts, cloneSearchRequest(base, core, "", ""))
			}
		}
	case KindPodcast:
		if core := coreTitle(base.Title); core != "" && !strings.EqualFold(core, base.Title) {
			attempts = append(attempts, cloneSearchRequest(base, core, "", ""))
		}
	}

	return dedupeSearchAttempts(attempts)
}

func cloneSearchRequest(base SearchRequest, title, author, query string) SearchRequest {
	clone := base
	clone.Title = strings.TrimSpace(title)
	clone.Author = strings.TrimSpace(author)
	clone.Query = strings.TrimSpace(query)
	if clone.Title != "" || clone.Author != "" {
		clone.Query = ""
	}
	return clone
}

func dedupeSearchAttempts(attempts []SearchRequest) []SearchRequest {
	seen := make(map[string]struct{}, len(attempts))
	out := make([]SearchRequest, 0, len(attempts))
	for _, attempt := range attempts {
		key := searchAttemptKey(attempt)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, attempt)
	}
	return out
}

func searchAttemptKey(request SearchRequest) string {
	return strings.ToLower(strings.Join([]string{
		string(request.Kind),
		request.Title,
		request.Author,
		request.Query,
		request.ISBN,
		request.ASIN,
		request.AudibleASIN,
	}, "|"))
}

func splitTitleAuthorQuery(query string) (title, author string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", ""
	}

	lower := strings.ToLower(query)
	if idx := strings.LastIndex(lower, " by "); idx > 0 {
		return strings.TrimSpace(query[:idx]), strings.TrimSpace(query[idx+4:])
	}

	parts := splitOnDash(query)
	if len(parts) >= 2 {
		start := 0
		if isTrackPrefix(parts[0]) {
			start = 1
		}
		if len(parts)-start >= 3 {
			return strings.TrimSpace(strings.Join(parts[start+1:], " - ")), strings.TrimSpace(parts[start])
		}
		if len(parts)-start == 2 {
			left := strings.TrimSpace(parts[start])
			right := strings.TrimSpace(parts[start+1])
			if len(left) > len(right)+8 {
				return left, right
			}
			if len(right) > len(left)+8 {
				return right, left
			}
			return right, left
		}
	}

	return query, ""
}

// inferTitleAuthorFromFreeText splits queries like "Breath James Nestor" where the
// user omitted "by". The last two tokens are treated as the author name.
func inferTitleAuthorFromFreeText(text string) (title, author string) {
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) < 3 {
		return "", ""
	}
	author = strings.Join(words[len(words)-2:], " ")
	title = strings.Join(words[:len(words)-2], " ")
	if title == "" || !looksLikePersonName(author) {
		return "", ""
	}
	return title, author
}

func looksLikePersonName(name string) bool {
	parts := strings.Fields(name)
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if len(part) < 2 {
			return false
		}
		digits := 0
		for _, r := range part {
			if unicode.IsDigit(r) {
				digits++
			}
		}
		if digits == len(part) {
			return false
		}
	}
	return true
}

func authorSurname(author string) string {
	parts := strings.Fields(strings.TrimSpace(author))
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func splitOnDash(value string) []string {
	raw := strings.Split(value, " - ")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func isTrackPrefix(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}

func cleanAudiobookTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	title = trackPrefixPattern.ReplaceAllString(title, "")
	title = bracketContentPattern.ReplaceAllString(title, " ")
	title = audiobookNoisePattern.ReplaceAllString(title, " ")
	title = strings.Join(strings.Fields(title), " ")
	return strings.TrimSpace(title)
}

func cleanPodcastTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	title = trackPrefixPattern.ReplaceAllString(title, "")
	title = bracketContentPattern.ReplaceAllString(title, " ")
	title = strings.Join(strings.Fields(title), " ")
	return strings.TrimSpace(title)
}

func coreTitle(title string) string {
	title = cleanAudiobookTitle(title)
	if title == "" {
		return ""
	}
	if idx := strings.Index(title, ":"); idx > 0 {
		title = strings.TrimSpace(title[:idx])
	}
	if idx := strings.Index(title, " - "); idx > 0 {
		left := strings.TrimSpace(title[:idx])
		right := strings.TrimSpace(title[idx+3:])
		if len(left) >= 4 && len(right) >= 4 {
			title = left
		}
	}
	return strings.TrimSpace(title)
}

func cleanPersonName(name string) string {
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, "audiobook") || strings.EqualFold(name, "podcast") {
		return ""
	}
	return name
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
