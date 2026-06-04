package metadata

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/scanner"
)

// AudnexusChapterProvider implements scanner.ChapterProvider by fetching
// authored chapter markers from Audnexus (the same source Audiobookshelf uses).
// It resolves an ASIN — preferring the one embedded in the book's tags, falling
// back to a title/author catalog search — then pulls
// /books/{asin}/chapters?region=…
//
// It deliberately wraps the existing AudibleProvider so the HTTP client, region
// handling, and ASIN search live in exactly one place.
type AudnexusChapterProvider struct {
	audible *AudibleProvider
}

// NewAudnexusChapterProvider builds a chapter provider over the given HTTP
// client (nil → a default client via the Audible provider) for the given
// Audible region (empty → "us").
func NewAudnexusChapterProvider(client *http.Client, region string) *AudnexusChapterProvider {
	return &AudnexusChapterProvider{audible: NewAudibleProvider(client).withRegion(region)}
}

// Chapters satisfies scanner.ChapterProvider. It returns a ChapterResult whose
// Outcome explains exactly what happened, so the scanner can log/persist why a
// book did or did not receive Audible chapters instead of silently keeping
// whatever the files yielded.
func (p *AudnexusChapterProvider) Chapters(ctx context.Context, lookup scanner.ChapterLookup) scanner.ChapterResult {
	if p == nil || p.audible == nil {
		return scanner.ChapterResult{Outcome: scanner.ChapterError, Detail: "chapter provider not initialised"}
	}

	asin, confidence, fail := p.resolveASIN(ctx, lookup)
	if asin == "" {
		return fail
	}

	chapters, outcome, detail := p.fetchChapters(ctx, asin)
	if len(chapters) == 0 {
		return scanner.ChapterResult{ASIN: asin, Outcome: outcome, Detail: detail}
	}

	// Final guard against a wrong-edition match that slipped through scoring: if
	// the markers' total runtime is wildly different from the files on disk they
	// won't line up, so keep the file-derived chapters. Checked on the RAW marker
	// runtime, before the rescale below re-anchors the end to the file.
	if !runtimeIsPlausible(chapters, lookup.DurationSeconds) {
		return scanner.ChapterResult{
			ASIN:    asin,
			Outcome: scanner.ChapterRuntimeReject,
			Detail: fmt.Sprintf("asin=%s coverage=%.0fs vs files=%.0fs",
				asin, chapters[len(chapters)-1].EndSeconds, lookup.DurationSeconds),
		}
	}

	return scanner.ChapterResult{
		Chapters: chapters,
		ASIN:     asin,
		Source:   scanner.ChapterSourceAudnexus,
		Outcome:  scanner.ChapterApplied,
		Detail:   fmt.Sprintf("asin=%s confidence=%.2f", asin, confidence),
	}
}

// resolveASIN returns the ASIN we will trust for this book together with a
// confidence in [0,1]. An ASIN embedded in the file tags is trusted outright. A
// title/author search is VERIFIED: each candidate edition is scored on title +
// author + runtime, and only a best match at/above audibleMatchThreshold is
// accepted — so a popular title can no longer silently resolve to the wrong
// edition. On failure it returns "" plus a fully-formed ChapterResult whose
// Outcome/Detail say why, which the caller returns as-is.
func (p *AudnexusChapterProvider) resolveASIN(ctx context.Context, lookup scanner.ChapterLookup) (string, float64, scanner.ChapterResult) {
	if asin := strings.ToUpper(strings.TrimSpace(lookup.ASIN)); isValidASIN(asin) {
		return asin, 1, scanner.ChapterResult{}
	}

	title := strings.TrimSpace(lookup.Title)
	if title == "" {
		return "", 0, scanner.ChapterResult{
			Outcome: scanner.ChapterNoASIN,
			Detail:  "no embedded ASIN and no title to search",
		}
	}

	asins, err := p.audible.searchCatalog(ctx, normalizeBookTitle(title), lookup.Author, audibleCandidateLimit)
	if err != nil {
		return "", 0, scanner.ChapterResult{
			Outcome: scanner.ChapterError,
			Detail:  "Audible catalog search failed: " + err.Error(),
		}
	}
	if len(asins) == 0 {
		return "", 0, scanner.ChapterResult{
			Outcome: scanner.ChapterNoASIN,
			Detail:  fmt.Sprintf("no Audible catalog hit for %q", title),
		}
	}

	asin, score := p.bestVerifiedCandidate(ctx, asins, lookup)
	if asin == "" || score < audibleMatchThreshold {
		return "", score, scanner.ChapterResult{
			Outcome: scanner.ChapterLowConfidence,
			Detail: fmt.Sprintf("best Audible match %.2f below threshold %.2f for %q",
				score, audibleMatchThreshold, title),
		}
	}
	return asin, score, scanner.ChapterResult{}
}

// bestVerifiedCandidate fetches and scores the top search hits, returning the
// best (ASIN, score). It stops early once a hit scores audibleMatchStrong so the
// common exact match costs a single book fetch.
func (p *AudnexusChapterProvider) bestVerifiedCandidate(ctx context.Context, asins []string, lookup scanner.ChapterLookup) (string, float64) {
	bestASIN, bestScore := "", 0.0
	for i, asin := range asins {
		if i >= audibleCandidateVerify {
			break
		}
		book, err := p.audible.fetchBook(ctx, asin)
		if err != nil {
			continue
		}
		if score := scoreAudibleCandidate(book, lookup); score > bestScore {
			bestScore, bestASIN = score, strings.ToUpper(strings.TrimSpace(book.ASIN))
			if bestScore >= audibleMatchStrong {
				break
			}
		}
	}
	return bestASIN, bestScore
}

func (p *AudnexusChapterProvider) fetchChapters(ctx context.Context, asin string) ([]catalog.AudioChapter, scanner.ChapterOutcome, string) {
	values := url.Values{}
	values.Set("region", p.audible.region)
	endpoint := withQuery(
		strings.TrimRight(p.audible.audnexusURL, "/")+"/books/"+url.PathEscape(asin)+"/chapters",
		values,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, scanner.ChapterError, "build chapters request: " + err.Error()
	}
	body, status, err := getJSONOptional[audnexusChapters](p.audible.client, req)
	if err != nil {
		return nil, scanner.ChapterError, fmt.Sprintf("chapters fetch for %s: %v", asin, err)
	}
	if status < 200 || status >= 300 {
		return nil, scanner.ChapterError, fmt.Sprintf("chapters fetch for %s: status %d", asin, status)
	}
	if len(body.Chapters) == 0 {
		return nil, scanner.ChapterNoChapters, fmt.Sprintf("Audnexus has no chapter markers for %s", asin)
	}

	// Audnexus flags markers it did NOT derive from the real audio (typically an
	// even split of the runtime). Those never fall on a sentence boundary, so
	// applying them is exactly what drops a chapter jump into the middle of a
	// sentence even though the timestamps "look right" on the seekbar. Treat
	// them as a miss and let the caller keep the accurate file-derived chapters.
	if body.IsAccurate != nil && !*body.IsAccurate {
		return nil, scanner.ChapterLowConfidence, fmt.Sprintf("Audnexus markers for %s flagged inaccurate", asin)
	}

	chapters := make([]catalog.AudioChapter, 0, len(body.Chapters))
	for index, marker := range body.Chapters {
		start := float64(marker.StartOffsetMs) / 1000
		end := float64(marker.StartOffsetMs+marker.LengthMs) / 1000
		title := strings.TrimSpace(marker.Title)
		if title == "" {
			title = "Chapter " + strconv.Itoa(index+1)
		}
		chapters = append(chapters, catalog.AudioChapter{
			Index:        index + 1,
			Title:        title,
			StartSeconds: start,
			EndSeconds:   end,
		})
	}
	return chapters, scanner.ChapterApplied, ""
}

// runtimeIsPlausible accepts the provider's chapters only when their coverage is
// within ~5% (or 2 min) of the on-disk runtime. A zero/unknown file duration
// passes — we have nothing to contradict the provider with.
func runtimeIsPlausible(chapters []catalog.AudioChapter, fileDurationSeconds float64) bool {
	if fileDurationSeconds <= 0 || len(chapters) == 0 {
		return true
	}
	coverage := chapters[len(chapters)-1].EndSeconds
	if coverage <= 0 {
		return true
	}
	tolerance := math.Max(120, fileDurationSeconds*0.05)
	return math.Abs(coverage-fileDurationSeconds) <= tolerance
}

type audnexusChapters struct {
	IsAccurate *bool             `json:"isAccurate"`
	Chapters   []audnexusChapter `json:"chapters"`
}

type audnexusChapter struct {
	StartOffsetMs int    `json:"startOffsetMs"`
	LengthMs      int    `json:"lengthMs"`
	Title         string `json:"title"`
}
