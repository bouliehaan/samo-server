package search

import (
	"strings"
	"unicode"
)

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

func MatchText(haystack, query string) bool {
	tokens := Tokenize(query)
	if len(tokens) == 0 {
		return true
	}
	haystack = strings.ToLower(haystack)
	for _, token := range tokens {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

func ScoreText(haystack, query string) int {
	tokens := Tokenize(query)
	if len(tokens) == 0 {
		return 0
	}
	haystack = strings.ToLower(strings.TrimSpace(haystack))
	score := 0
	for index, token := range tokens {
		position := strings.Index(haystack, token)
		if position < 0 {
			return -1
		}
		score += 100 - index*5 - position
		if position == 0 && index == 0 {
			score += 50
		}
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
