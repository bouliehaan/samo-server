package search

import (
	"sort"
	"strings"
)

// The filter is deliberately loose — every query word has to appear somewhere
// in the record, as a substring (MatchText) — so for "kiss me" it admits "Kiss
// Me", "Kiss Me Quick", "Kiss me where it smells funny" and every track on an
// album called "Kiss Me Kiss Me Kiss Me". Ranking is what puts the one the
// user typed first.
//
// The title decides. It is tried against a ladder of tiers, top down, and the
// first tier that fits is the score, plus a coverage bonus that orders titles
// within a tier. Tiers sit 100 apart and coverage tops out at 99, so a long
// title can never climb past a shorter one that matched on a higher tier:
// "Kiss Me" beats "Kiss Me Quick" beats "Kiss me where it smells funny".
//
// Everything else the record carries — artist, album, genres, description —
// only matters when the title matched nothing at all. It then scores as a
// floor, below every title tier, so no amount of album or artist text can
// lift a record over one whose title fits the query.
const (
	tierExact        = 1000 // the title is the query
	tierPrefix       = 800  // the title starts with the query: "Kiss Me Quick"
	tierPhrase       = 700  // the query appears whole, on word boundaries: "Please Kiss Me"
	tierTitleInQuery = 600  // the whole title appears in the query: "kiss me sixpence" → "Kiss Me"
	tierWords        = 500  // every query word is a word of the title, in any order
	tierSubstrings   = 300  // every query word appears somewhere in the title: "Kissing Men"
	tierPartial      = 100  // scaled by the share of query words the title has, so at most 99
	maxCoverage      = 99   // on tierSubstrings and up: the share of the longer string the match accounts for

	floorPhrase = 80 // no title match; the query appears whole in the secondary text
	floorWords  = 60 // no title match; scaled by the share of query words the secondary text has
)

// relevance is the query, prepared once per search.
type relevance struct {
	// phrases holds the query normalised the same way titles are, then the
	// same with stopwords and single letters dropped when that differs:
	// "the kiss" → ["the kiss", "kiss"]. A title is tried against each.
	phrases []string
	// words are the significant query words: what the word-level tiers count.
	words []string
}

func newRelevance(query string) relevance {
	rel := relevance{words: significantTokens(query)}
	full := strings.Join(Tokenize(query), " ")
	if full == "" {
		return rel
	}
	rel.phrases = []string{full}
	if significant := strings.Join(rel.words, " "); significant != full {
		rel.phrases = append(rel.phrases, significant)
	}
	return rel
}

// normalizeText lowercases and reduces every punctuation run to one space,
// which is exactly how Tokenize reads the query, so a title and a query that
// differ only in case or punctuation compare equal.
func normalizeText(value string) string {
	return strings.Join(Tokenize(value), " ")
}

// score ranks one record. Zero means the text played no part in the match,
// which only happens when the query carries no words (filter-only searches).
func (r relevance) score(title, secondary string) int {
	if len(r.phrases) == 0 {
		return 0
	}
	title = normalizeText(title)
	secondary = normalizeText(secondary)

	if titleScore := r.scoreTitle(title, secondary); titleScore > 0 {
		return titleScore
	}
	return r.scoreSecondary(secondary)
}

// scoreTitle is the title's tier plus coverage; zero when the title has no
// significant query word at all.
func (r relevance) scoreTitle(title, secondary string) int {
	if title == "" {
		return 0
	}
	// A leading article is dropped for the phrase tiers (the word tiers see
	// the full title), so "The Kiss" still counts as an exact title for the
	// query "kiss" — below the track actually called "Kiss", because coverage
	// still sees the full title.
	titles := []string{title}
	if bare := withoutLeadingArticle(title); bare != title {
		titles = append(titles, bare)
	}
	full := r.phrases[0]

	// The query inside the title, best tier first. Every title form is tried
	// against every phrase form before the next tier is considered, so a
	// lower tier on the full phrase never beats a higher one on the
	// significant phrase.
	type phraseTier struct {
		tier int
		hit  func(title, phrase string) bool
	}
	for _, step := range []phraseTier{
		{tierExact, func(t, p string) bool { return t == p }},
		{tierPrefix, func(t, p string) bool { return strings.HasPrefix(t, p+" ") }},
		{tierPhrase, func(t, p string) bool { return strings.Contains(" "+t+" ", " "+p+" ") }},
	} {
		for _, candidate := range titles {
			for _, phrase := range r.phrases {
				if step.hit(candidate, phrase) {
					return step.tier + coverage(len(phrase), len(title), len(full))
				}
			}
		}
	}

	// The whole title inside the query: the user typed a title they knew and
	// added the artist or album ("hello adele", "kiss me sixpence"). That
	// reading only holds if the record really carries what they added, as
	// whole words; otherwise "Kiss" for "kiss me" is just a partial title.
	for _, candidate := range titles {
		for _, phrase := range r.phrases {
			if !strings.Contains(" "+phrase+" ", " "+candidate+" ") {
				continue
			}
			if allWordsIn(secondary, r.wordsNotIn(candidate)) {
				return tierTitleInQuery + coverage(len(candidate), len(title), len(full))
			}
		}
	}

	// Word tiers: how many significant query words the title has, and how.
	titleWords := strings.Fields(title)
	whole, anywhere, matchedChars := 0, 0, 0
	for _, word := range r.words {
		switch {
		case containsWord(titleWords, word):
			whole++
			anywhere++
			matchedChars += len(word)
		case strings.Contains(title, word):
			anywhere++
			matchedChars += len(word)
		}
	}
	switch {
	case anywhere == 0:
		return 0
	case whole == len(r.words):
		return tierWords + coverage(matchedChars, len(title), len(full))
	case anywhere == len(r.words):
		return tierSubstrings + coverage(matchedChars, len(title), len(full))
	default:
		return tierPartial * anywhere / len(r.words)
	}
}

// wordsNotIn is the significant query words a title did not contain — the
// artist or album the user appended to a title they knew.
func (r relevance) wordsNotIn(title string) []string {
	leftover := make([]string, 0, len(r.words))
	for _, word := range r.words {
		if !strings.Contains(title, word) {
			leftover = append(leftover, word)
		}
	}
	return leftover
}

// allWordsIn reports whether every word occurs in text as a whole word.
func allWordsIn(text string, words []string) bool {
	fields := strings.Fields(text)
	for _, word := range words {
		if !containsWord(fields, word) {
			return false
		}
	}
	return true
}

// scoreSecondary is the floor for a record whose title matched nothing: the
// query as a whole phrase in the artist, album or description text, or
// failing that, the share of query words found there.
func (r relevance) scoreSecondary(secondary string) int {
	if secondary == "" {
		return 0
	}
	padded := " " + secondary + " "
	for _, phrase := range r.phrases {
		if strings.Contains(padded, " "+phrase+" ") {
			return floorPhrase
		}
	}
	found := 0
	for _, word := range r.words {
		if strings.Contains(secondary, word) {
			found++
		}
	}
	return floorWords * found / len(r.words)
}

// coverage is the share of the longer of title and query that the match
// accounts for, as a bonus of at most maxCoverage. It is what orders titles
// within a tier: for "kiss me", "Kiss Me Quick" covers more of itself than
// "Kiss me where it smells funny" does.
func coverage(matched, titleLen, queryLen int) int {
	longest := max(titleLen, queryLen)
	if longest <= 0 || matched <= 0 {
		return 0
	}
	return min(maxCoverage, maxCoverage*matched/longest)
}

var leadingArticles = []string{"the ", "a ", "an "}

func withoutLeadingArticle(title string) string {
	for _, article := range leadingArticles {
		if rest, ok := strings.CutPrefix(title, article); ok && rest != "" {
			return rest
		}
	}
	return title
}

func containsWord(words []string, want string) bool {
	for _, word := range words {
		if word == want {
			return true
		}
	}
	return false
}

// rankFields is what relevance ordering reads from a record.
type rankFields struct {
	Title     string
	Secondary string
	Plays     int
}

// sortByRelevance orders items best match first. Equal scores fall back to
// the user's play count and then the title, so of five identically named
// tracks the one they actually listen to comes first, and a search that
// carries no text still comes out in a meaningful order.
//
// Everything the comparator needs is derived once per item up front; the
// comparator this replaces re-joined the record's search text and
// re-tokenised the query on every comparison.
func sortByRelevance[T any](items []T, query string, fields func(T) rankFields) {
	rel := newRelevance(query)
	type entry struct {
		index int
		score int
		plays int
		title string
	}
	entries := make([]entry, len(items))
	for i, item := range items {
		f := fields(item)
		entries[i] = entry{
			index: i,
			score: rel.score(f.Title, f.Secondary),
			plays: f.Plays,
			title: strings.ToLower(f.Title),
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := &entries[i], &entries[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.plays != b.plays {
			return a.plays > b.plays
		}
		return a.title < b.title
	})
	ordered := make([]T, len(items))
	for i, e := range entries {
		ordered[i] = items[e.index]
	}
	copy(items, ordered)
}
