package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/scanner"
)

func TestAudnexusChapterProviderFetchesByASIN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/books/B0000000XX/chapters" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chapters":[
			{"title":"Opening Credits","startOffsetMs":0,"lengthMs":15000},
			{"title":"Chapter 1","startOffsetMs":15000,"lengthMs":600000},
			{"title":"Chapter 2","startOffsetMs":615000,"lengthMs":585000}
		]}`))
	}))
	defer server.Close()

	provider := NewAudnexusChapterProvider(server.Client(), "us")
	provider.audible.audnexusURL = server.URL

	result := provider.Chapters(context.Background(), scanner.ChapterLookup{
		ASIN:            "B0000000XX",
		DurationSeconds: 1200,
	})
	if result.Outcome != scanner.ChapterApplied {
		t.Fatalf("outcome = %q, want applied (%s)", result.Outcome, result.Detail)
	}
	if result.Source != scanner.ChapterSourceAudnexus {
		t.Fatalf("source = %q, want %q", result.Source, scanner.ChapterSourceAudnexus)
	}
	if result.ASIN != "B0000000XX" {
		t.Fatalf("asin = %q, want B0000000XX", result.ASIN)
	}
	if len(result.Chapters) != 3 {
		t.Fatalf("chapters = %d, want 3", len(result.Chapters))
	}
	if result.Chapters[1].Title != "Chapter 1" {
		t.Fatalf("chapter[1].Title = %q, want Chapter 1", result.Chapters[1].Title)
	}
	if result.Chapters[1].StartSeconds != 15 {
		t.Fatalf("chapter[1].StartSeconds = %v, want 15", result.Chapters[1].StartSeconds)
	}
	if result.Chapters[2].EndSeconds != 1200 {
		t.Fatalf("chapter[2].EndSeconds = %v, want 1200", result.Chapters[2].EndSeconds)
	}
}

func TestAudnexusChapterProviderRejectsRuntimeMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"chapters":[{"title":"Only","startOffsetMs":0,"lengthMs":120000}]}`))
	}))
	defer server.Close()

	provider := NewAudnexusChapterProvider(server.Client(), "us")
	provider.audible.audnexusURL = server.URL

	// Provider says 120s of chapters, but the files on disk are ~2h — wrong
	// edition, so the markers must be rejected (and the reason recorded).
	result := provider.Chapters(context.Background(), scanner.ChapterLookup{
		ASIN:            "B0000000XX",
		DurationSeconds: 7200,
	})
	if result.Outcome != scanner.ChapterRuntimeReject {
		t.Fatalf("outcome = %q, want runtime-reject", result.Outcome)
	}
	if len(result.Chapters) != 0 {
		t.Fatalf("chapters = %d, want 0 on rejection", len(result.Chapters))
	}
}

// TestAudnexusChapterProviderAppliesRawOffsets documents the ABS-aligned
// behavior: Audnexus markers within the plausible range are applied VERBATIM —
// never linearly rescaled to force-fit the file duration (a hack that smears
// every boundary). Real chapter alignment comes from trusting on-disk markers;
// Audnexus is only consulted when the files carry none.
func TestAudnexusChapterProviderAppliesRawOffsets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chapters":[
			{"title":"Chapter 1","startOffsetMs":0,"lengthMs":600000},
			{"title":"Chapter 2","startOffsetMs":600000,"lengthMs":650000}
		]}`))
	}))
	defer server.Close()

	provider := NewAudnexusChapterProvider(server.Client(), "us")
	provider.audible.audnexusURL = server.URL

	// File is 1200s; markers cover 1250s (within the ~5% plausibility window).
	result := provider.Chapters(context.Background(), scanner.ChapterLookup{
		ASIN:            "B0000000XX",
		DurationSeconds: 1200,
	})
	if result.Outcome != scanner.ChapterApplied {
		t.Fatalf("outcome = %q, want applied (%s)", result.Outcome, result.Detail)
	}
	// Offsets are preserved EXACTLY — not rescaled toward 1200.
	if result.Chapters[1].StartSeconds != 600 {
		t.Fatalf("chapter[1].StartSeconds = %v, want 600 (raw, not rescaled)", result.Chapters[1].StartSeconds)
	}
	if result.Chapters[1].EndSeconds != 1250 {
		t.Fatalf("chapter[1].EndSeconds = %v, want 1250 (raw, not rescaled)", result.Chapters[1].EndSeconds)
	}
}

// TestAudnexusChapterProviderRejectsInaccurateMarkers covers Audnexus markers
// flagged isAccurate=false (an even runtime split, not real chapter data). Those
// never fall on a sentence boundary, so they must be rejected and the accurate
// file-derived chapters kept.
func TestAudnexusChapterProviderRejectsInaccurateMarkers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"isAccurate":false,"chapters":[
			{"title":"Chapter 1","startOffsetMs":0,"lengthMs":600000},
			{"title":"Chapter 2","startOffsetMs":600000,"lengthMs":600000}
		]}`))
	}))
	defer server.Close()

	provider := NewAudnexusChapterProvider(server.Client(), "us")
	provider.audible.audnexusURL = server.URL

	result := provider.Chapters(context.Background(), scanner.ChapterLookup{
		ASIN:            "B0000000XX",
		DurationSeconds: 1200,
	})
	if result.Outcome != scanner.ChapterLowConfidence {
		t.Fatalf("outcome = %q, want low-confidence for inaccurate markers", result.Outcome)
	}
	if len(result.Chapters) != 0 {
		t.Fatalf("chapters = %d, want 0 when markers are flagged inaccurate", len(result.Chapters))
	}
}

func TestAudnexusChapterProviderNoASIN(t *testing.T) {
	provider := NewAudnexusChapterProvider(nil, "us")
	result := provider.Chapters(context.Background(), scanner.ChapterLookup{Title: ""})
	if result.Outcome != scanner.ChapterNoASIN {
		t.Fatalf("outcome = %q, want no-asin when nothing identifies the book", result.Outcome)
	}
	if len(result.Chapters) != 0 {
		t.Fatalf("chapters = %d, want 0", len(result.Chapters))
	}
}

// TestAudnexusChapterProviderResolvesByTitleSearch covers the path with NO
// embedded ASIN: a verified title/author/runtime match must resolve to the
// right edition and apply its chapters.
func TestAudnexusChapterProviderResolvesByTitleSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/catalog"):
			_, _ = w.Write([]byte(`{"products":[{"asin":"B0SEARCH01"},{"asin":"B0SEARCH02"}]}`))
		case r.URL.Path == "/books/B0SEARCH01/chapters":
			_, _ = w.Write([]byte(`{"chapters":[
				{"title":"Chapter 1","startOffsetMs":0,"lengthMs":600000},
				{"title":"Chapter 2","startOffsetMs":600000,"lengthMs":600000}
			]}`))
		case r.URL.Path == "/books/B0SEARCH01":
			_, _ = w.Write([]byte(`{"asin":"B0SEARCH01","title":"Project Hail Mary","runtimeLengthMin":20,"authors":[{"name":"Andy Weir"}]}`))
		case r.URL.Path == "/books/B0SEARCH02":
			_, _ = w.Write([]byte(`{"asin":"B0SEARCH02","title":"Some Unrelated Book","runtimeLengthMin":90,"authors":[{"name":"Other Person"}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewAudnexusChapterProvider(server.Client(), "us")
	provider.audible.audnexusURL = server.URL
	provider.audible.catalogProductsURL = server.URL + "/catalog"

	result := provider.Chapters(context.Background(), scanner.ChapterLookup{
		Title:           "Project Hail Mary: A Novel (Unabridged)",
		Author:          "Andy Weir",
		DurationSeconds: 1200,
	})
	if result.Outcome != scanner.ChapterApplied {
		t.Fatalf("outcome = %q, want applied (%s)", result.Outcome, result.Detail)
	}
	if result.ASIN != "B0SEARCH01" {
		t.Fatalf("asin = %q, want B0SEARCH01 (the verified edition)", result.ASIN)
	}
	if len(result.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(result.Chapters))
	}
}

// TestAudnexusChapterProviderLowConfidence covers a title search that only
// returns a clearly-wrong edition: it must be REJECTED (low-confidence), not
// silently stamped onto the book.
func TestAudnexusChapterProviderLowConfidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/catalog"):
			_, _ = w.Write([]byte(`{"products":[{"asin":"B0WRONG001"}]}`))
		case r.URL.Path == "/books/B0WRONG001":
			_, _ = w.Write([]byte(`{"asin":"B0WRONG001","title":"A Completely Different Title","runtimeLengthMin":600,"authors":[{"name":"Nobody Relevant"}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewAudnexusChapterProvider(server.Client(), "us")
	provider.audible.audnexusURL = server.URL
	provider.audible.catalogProductsURL = server.URL + "/catalog"

	result := provider.Chapters(context.Background(), scanner.ChapterLookup{
		Title:           "Project Hail Mary",
		Author:          "Andy Weir",
		DurationSeconds: 1200,
	})
	if result.Outcome != scanner.ChapterLowConfidence {
		t.Fatalf("outcome = %q, want low-confidence", result.Outcome)
	}
	if len(result.Chapters) != 0 {
		t.Fatalf("chapters = %d, want 0 when no edition verifies", len(result.Chapters))
	}
}
