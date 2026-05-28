package search

import (
	"math"
	"strings"
	"unicode"
)

var searchStopwords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "at": {}, "by": {}, "for": {}, "from": {}, "in": {},
	"of": {}, "on": {}, "or": {}, "the": {}, "to": {}, "with": {},
}

func Tokenize(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			tokens = append(tokens, field)
		}
	}
	return tokens
}

func significantTokens(query string) []string {
	tokens := Tokenize(query)
	if len(tokens) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if len(token) < 2 {
			continue
		}
		if _, skip := searchStopwords[token]; skip {
			continue
		}
		filtered = append(filtered, token)
	}
	if len(filtered) > 0 {
		return filtered
	}
	return tokens
}

func MatchText(haystack, query string) bool {
	tokens := significantTokens(query)
	if len(tokens) == 0 {
		return true
	}
	haystack = strings.ToLower(haystack)
	if len(tokens) <= 3 {
		for _, token := range tokens {
			if !strings.Contains(haystack, token) {
				return false
			}
		}
		return true
	}
	matched := 0
	for _, token := range tokens {
		if strings.Contains(haystack, token) {
			matched++
		}
	}
	required := int(math.Ceil(float64(len(tokens)) * 0.65))
	if required < 2 {
		required = 2
	}
	return matched >= required
}

func ScoreText(haystack, query string) int {
	tokens := significantTokens(query)
	if len(tokens) == 0 {
		return 0
	}
	haystack = strings.ToLower(strings.TrimSpace(haystack))
	score := 0
	matched := 0
	for index, token := range tokens {
		position := strings.Index(haystack, token)
		if position < 0 {
			continue
		}
		matched++
		score += 100 - index*5 - position
		if position == 0 && index == 0 {
			score += 50
		}
	}
	if matched == 0 {
		return -1
	}
	required := len(tokens)
	if len(tokens) > 3 {
		required = int(math.Ceil(float64(len(tokens)) * 0.65))
	}
	if matched < required {
		return -1
	}
	return score
}

func joinFields(values ...string) string {
	return strings.ToLower(strings.TrimSpace(strings.Join(values, " ")))
}

func genreMatches(genres []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return true
	}
	for _, genre := range genres {
		if strings.EqualFold(strings.TrimSpace(genre), want) {
			return true
		}
	}
	return false
}
