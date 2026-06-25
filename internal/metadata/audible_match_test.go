package metadata

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/scanner"
)

func TestNormalizeBookTitle(t *testing.T) {
	cases := map[string]string{
		"Project Hail Mary: A Novel (Unabridged)": "project hail mary a novel",
		"The Way of Kings [Stormlight #1]":        "the way of kings",
		"  Dune   ":                               "dune",
	}
	for in, want := range cases {
		if got := normalizeBookTitle(in); got != want {
			t.Errorf("normalizeBookTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTitleSimilarity(t *testing.T) {
	if got := titleSimilarity("Project Hail Mary", "Project Hail Mary"); got < 0.99 {
		t.Errorf("identical titles = %.2f, want ~1", got)
	}
	// Audible title is a clean subset of a noisy file title → still near 1.
	if got := titleSimilarity("Project Hail Mary", "Project Hail Mary: A Novel (Unabridged)"); got < 0.9 {
		t.Errorf("subset title = %.2f, want >= 0.9", got)
	}
	if got := titleSimilarity("Project Hail Mary", "The Martian"); got > 0.2 {
		t.Errorf("disjoint titles = %.2f, want low", got)
	}
}

func TestRuntimeProximity(t *testing.T) {
	if got := runtimeProximity(3600, 3600); got != 1 {
		t.Errorf("exact runtime = %.2f, want 1", got)
	}
	if got := runtimeProximity(3780, 3600); got < 0.49 || got > 0.51 { // 5% diff → ~0.5
		t.Errorf("5%% runtime = %.2f, want ~0.5", got)
	}
	if got := runtimeProximity(7200, 3600); got != 0 { // 100% diff → 0
		t.Errorf("double runtime = %.2f, want 0", got)
	}
	if got := runtimeProximity(0, 3600); got != 1 {
		t.Errorf("unknown candidate runtime = %.2f, want neutral 1", got)
	}
}

func TestScoreAudibleCandidate(t *testing.T) {
	lookup := scanner.ChapterLookup{
		Title:           "Project Hail Mary: A Novel (Unabridged)",
		Author:          "Andy Weir",
		DurationSeconds: 3600,
	}

	good := audnexusBook{
		Title:            "Project Hail Mary",
		RuntimeLengthMin: 60,
		Authors:          []audnexusPerson{{Name: "Andy Weir"}},
	}
	if score := scoreAudibleCandidate(good, lookup); score < audibleMatchThreshold {
		t.Errorf("good candidate scored %.2f, want >= %.2f", score, audibleMatchThreshold)
	}

	wrong := audnexusBook{
		Title:            "An Entirely Different Book",
		RuntimeLengthMin: 600,
		Authors:          []audnexusPerson{{Name: "Someone Else"}},
	}
	if score := scoreAudibleCandidate(wrong, lookup); score >= audibleMatchThreshold {
		t.Errorf("wrong candidate scored %.2f, want < %.2f", score, audibleMatchThreshold)
	}
}

func TestScoreAudibleCandidateNoAuthor(t *testing.T) {
	// With no author to compare, a strong title + runtime match must still pass.
	lookup := scanner.ChapterLookup{Title: "Dune", DurationSeconds: 3600}
	book := audnexusBook{Title: "Dune", RuntimeLengthMin: 60}
	if score := scoreAudibleCandidate(book, lookup); score < audibleMatchThreshold {
		t.Errorf("authorless match scored %.2f, want >= %.2f", score, audibleMatchThreshold)
	}
}
