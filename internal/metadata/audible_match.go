package metadata

import (
	"math"
	"regexp"
	"strings"

	"github.com/bouliehaan/samo-server/internal/scanner"
)

// Tuning for verified Audible matching. The point is to make a title/author
// search trustworthy enough that its chapters can REPLACE the embedded ones, so
// the bar is deliberately conservative: we would rather keep file chapters than
// stamp a book with a wrong edition's markers.
const (
	audibleCandidateLimit  = 10   // search hits to request from the Audible catalog
	audibleCandidateVerify = 5    // of those, how many to deep-verify (one book fetch each)
	audibleMatchThreshold  = 0.55 // minimum blended score to accept a match
	audibleMatchStrong     = 0.90 // score at which we stop verifying further candidates
)

var (
	bracketedTextPattern = regexp.MustCompile(`[\(\[][^\)\]]*[\)\]]`)
	nonAlphanumPattern   = regexp.MustCompile(`[^a-z0-9]+`)
)

// titleStopWords are dropped before comparing titles so subtitle filler
// ("Title: A Novel") doesn't drag similarity down.
var titleStopWords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "of": {}, "novel": {},
	"unabridged": {}, "abridged": {}, "audiobook": {},
}

// scoreAudibleCandidate blends title, author, and runtime agreement into a
// single confidence in [0,1]. Title carries the most weight; runtime guards
// against same-title different-work/edition; author breaks ties when present.
func scoreAudibleCandidate(book audnexusBook, lookup scanner.ChapterLookup) float64 {
	title := titleSimilarity(book.Title, lookup.Title)
	runtime := runtimeProximity(float64(book.RuntimeLengthMin)*60, lookup.DurationSeconds)

	author, haveAuthor := authorSimilarity(book.Authors, lookup.Author)
	if !haveAuthor {
		// Nothing to compare authors against — lean on title + runtime only.
		return clamp01(0.65*title + 0.35*runtime)
	}
	return clamp01(0.5*title + 0.2*author + 0.3*runtime)
}

// titleSimilarity compares two titles by normalized word sets, combining
// Jaccard with containment so an Audible title that is a clean subset of a
// noisier file title ("Project Hail Mary" vs "Project Hail Mary: A Novel")
// still scores near 1.
func titleSimilarity(a, b string) float64 {
	at, bt := titleTokens(a), titleTokens(b)
	if len(at) == 0 || len(bt) == 0 {
		return 0
	}
	inter := intersectionCount(at, bt)
	if inter == 0 {
		return 0
	}
	union := len(at) + len(bt) - inter
	jaccard := float64(inter) / float64(union)
	containment := float64(inter) / float64(minInt(len(at), len(bt)))
	// Discount containment slightly so an exact match still beats a subset.
	return math.Max(jaccard, containment*0.95)
}

// authorSimilarity returns the best containment of the wanted author name
// across the candidate's authors, and whether there was anything to compare.
func authorSimilarity(authors []audnexusPerson, want string) (float64, bool) {
	wantTokens := nameTokens(want)
	if len(wantTokens) == 0 {
		return 0, false
	}
	best := 0.0
	for _, person := range authors {
		at := nameTokens(person.Name)
		if len(at) == 0 {
			continue
		}
		inter := intersectionCount(wantTokens, at)
		c := float64(inter) / float64(minInt(len(wantTokens), len(at)))
		if c > best {
			best = c
		}
	}
	return best, true
}

// runtimeProximity scores how close two runtimes are: 1.0 at an exact match,
// falling linearly to 0 at a 10% difference. Unknown runtimes are neutral (1.0)
// because we have nothing to contradict the candidate with.
func runtimeProximity(candidateSeconds, fileSeconds float64) float64 {
	if fileSeconds <= 0 || candidateSeconds <= 0 {
		return 1
	}
	diff := math.Abs(candidateSeconds-fileSeconds) / fileSeconds
	return clamp01(1 - diff/0.10)
}

// normalizeBookTitle lowercases, strips bracketed asides ("(Unabridged)") and
// punctuation, and collapses whitespace. Used both to clean a noisy title
// before searching and as the basis for title tokens.
func normalizeBookTitle(s string) string {
	s = strings.ToLower(s)
	s = bracketedTextPattern.ReplaceAllString(s, " ")
	s = nonAlphanumPattern.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func titleTokens(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, tok := range strings.Fields(normalizeBookTitle(s)) {
		if _, stop := titleStopWords[tok]; stop {
			continue
		}
		out[tok] = struct{}{}
	}
	return out
}

func nameTokens(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, tok := range strings.Fields(normalizeBookTitle(s)) {
		out[tok] = struct{}{}
	}
	return out
}

func intersectionCount(a, b map[string]struct{}) int {
	// Iterate the smaller set for fewer lookups.
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	count := 0
	for tok := range small {
		if _, ok := large[tok]; ok {
			count++
		}
	}
	return count
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}
