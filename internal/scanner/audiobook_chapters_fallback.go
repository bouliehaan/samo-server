package scanner

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Provenance labels for the chapters the files themselves yielded. They record
// what we fall back TO when Audible cannot be verified, so a weak book is
// queryable instead of invisible.
const (
	chapterSourceEmbedded = "embedded" // real intra-file markers (chpl/ffmetadata/overdrive)
	chapterSourceCue      = "cue"      // markers read from a sidecar .cue
	chapterSourceFile     = "file"     // degenerate one-chapter-per-file (navigationally useless)
	chapterSourceNone     = "none"     // no chapters anywhere
)

// externalChaptersSafe calls the configured chapter provider but never lets a
// provider bug (a nil HTTP client, a malformed response, a panicking decode)
// take down the whole scan. On panic it logs and returns a ChapterError result
// — NOT a silent nil — so the caller still records that the book fell back and
// why, then moves on to the next book.
func (s *Scanner) externalChaptersSafe(
	ctx context.Context,
	item catalog.AudiobookItem,
	duration int,
) (result ChapterResult) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("scanner: external chapter provider panicked, keeping file chapters: %v", r)
			result = ChapterResult{Outcome: ChapterError, Detail: fmt.Sprintf("panic: %v", r)}
		}
	}()
	return s.chapterProvider.Chapters(ctx, s.chapterLookup(item, duration))
}

// isOneChapterPerFile reports the degenerate shape flattenBookChapters produces
// for a multi-file book whose files carry NO embedded markers: exactly one
// chapter per file, every boundary sitting on a file boundary. That is not real
// chapter structure — each "chapter" is just a whole file — so it is labelled
// weak "file" provenance. (It no longer gates the network call: with
// Audible-first we ask the provider for every book and let verification decide.)
func isOneChapterPerFile(chapters []catalog.AudioChapter, probes []probedFile) bool {
	if len(probes) <= 1 || len(chapters) == 0 || len(chapters) != len(probes) {
		return false
	}
	offset := 0.0
	for i, probe := range probes {
		if !floatsClose(chapters[i].StartSeconds, offset, 1.5) {
			return false
		}
		offset += probeDurationSeconds(probe)
	}
	return true
}

func floatsClose(a, b, tolerance float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tolerance
}

// chapterLookup builds the identifying info handed to a ChapterProvider from the
// audiobook's metadata.
func (s *Scanner) chapterLookup(item catalog.AudiobookItem, durationSeconds int) ChapterLookup {
	lookup := ChapterLookup{DurationSeconds: float64(durationSeconds)}
	if item.Book != nil {
		lookup.ASIN = firstNonEmpty(
			strings.TrimSpace(item.Book.ExternalIDs.AudibleASIN),
			strings.TrimSpace(item.Book.ExternalIDs.ASIN),
		)
		lookup.Title = strings.TrimSpace(item.Book.Title)
		if len(item.Book.Authors) > 0 {
			lookup.Author = strings.TrimSpace(item.Book.Authors[0].Name)
		}
	}
	return lookup
}
