package metadata

import "testing"

func TestPrepareAudiobookSearchRequestSplitsCombinedQuery(t *testing.T) {
	request := prepareAudiobookSearchRequest(SearchRequest{
		Kind:  KindAudiobook,
		Query: "The Hobbit by J.R.R. Tolkien",
	})
	if request.Title != "The Hobbit" {
		t.Fatalf("title = %q, want The Hobbit", request.Title)
	}
	if request.Author != "J.R.R. Tolkien" {
		t.Fatalf("author = %q, want J.R.R. Tolkien", request.Author)
	}
	if request.Query != "" {
		t.Fatalf("query = %q, want empty when structured fields exist", request.Query)
	}
}

func TestPrepareAudiobookSearchRequestCleansNoise(t *testing.T) {
	request := prepareAudiobookSearchRequest(SearchRequest{
		Kind:   KindAudiobook,
		Title:  "01 - Dune [Audiobook] (Unabridged)",
		Author: "Frank Herbert",
	})
	if request.Title != "Dune" {
		t.Fatalf("title = %q, want Dune", request.Title)
	}
}

func TestPrepareAudiobookSearchRequestIgnoresPlaceholderAuthor(t *testing.T) {
	request := prepareAudiobookSearchRequest(SearchRequest{
		Kind:   KindAudiobook,
		Title:  "Project Hail Mary",
		Author: "AUDIOBOOK",
	})
	if request.Author != "" {
		t.Fatalf("author = %q, want empty", request.Author)
	}
}

func TestSearchAttemptsIncludeTitleOnlyFallback(t *testing.T) {
	attempts := searchAttempts(SearchRequest{
		Kind:   KindAudiobook,
		Title:  "Project Hail Mary",
		Author: "Andy Weir",
		Limit:  5,
	})
	if len(attempts) < 2 {
		t.Fatalf("attempts = %d, want at least 2", len(attempts))
	}
	foundTitleOnly := false
	for _, attempt := range attempts[1:] {
		if attempt.Title == "Project Hail Mary" && attempt.Author == "" {
			foundTitleOnly = true
			break
		}
	}
	if !foundTitleOnly {
		t.Fatal("expected a title-only fallback attempt")
	}
}

func TestPrepareAudiobookSearchRequestInfersAuthorFromFreeText(t *testing.T) {
	request := prepareAudiobookSearchRequest(SearchRequest{
		Kind:  KindAudiobook,
		Query: "Breath James Nestor",
	})
	if request.Title != "Breath" {
		t.Fatalf("title = %q, want Breath", request.Title)
	}
	if request.Author != "James Nestor" {
		t.Fatalf("author = %q, want James Nestor", request.Author)
	}
}

func TestSearchAttemptsIncludeAuthorSurnameFallback(t *testing.T) {
	attempts := searchAttempts(SearchRequest{
		Kind:   KindAudiobook,
		Title:  "Breath",
		Author: "James Nestor",
		Limit:  5,
	})
	found := false
	for _, attempt := range attempts {
		if attempt.Title == "Breath" && attempt.Author == "Nestor" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected author surname fallback attempt")
	}
}

func TestSplitTitleAuthorQueryFilenamePattern(t *testing.T) {
	title, author := splitTitleAuthorQuery("01 - Andy Weir - Project Hail Mary")
	if title != "Project Hail Mary" {
		t.Fatalf("title = %q", title)
	}
	if author != "Andy Weir" {
		t.Fatalf("author = %q", author)
	}
}
