package scanner

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func TestNormalizeAudiobookChaptersCollapsesShortOverdriveMarkers(t *testing.T) {
	probe := probedFile{
		AudioFile: catalog.AudioFile{
			Path:            "/books/Nestor/Breath.m4b",
			DurationSeconds: 36000,
		},
		Tags: catalog.Tags{"title": []string{"Breath"}},
	}
	chapters := []catalog.AudioChapter{
		{Index: 1, Title: "Intro", StartSeconds: 0, EndSeconds: 30},
		{Index: 2, Title: "Opening Credits", StartSeconds: 30, EndSeconds: 90},
	}
	out := normalizeAudiobookChapters([]probedFile{probe}, chapters)
	if len(out) != 1 {
		t.Fatalf("chapters = %d, want 1 full-book chapter", len(out))
	}
	if out[0].EndSeconds != 36000 {
		t.Fatalf("end = %v, want 36000", out[0].EndSeconds)
	}
}

func TestNormalizeAudiobookChaptersFixesLastChapterEnd(t *testing.T) {
	probe := probedFile{
		AudioFile: catalog.AudioFile{DurationSeconds: 1000},
	}
	chapters := []catalog.AudioChapter{
		{Index: 1, Title: "One", StartSeconds: 0, EndSeconds: 400},
		{Index: 2, Title: "Two", StartSeconds: 400, EndSeconds: 0},
	}
	out := normalizeAudiobookChapters([]probedFile{probe}, chapters)
	if out[len(out)-1].EndSeconds != 1000 {
		t.Fatalf("last end = %v, want 1000", out[len(out)-1].EndSeconds)
	}
}
