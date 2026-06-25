package scanner

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func twoFileProbes() []probedFile {
	return []probedFile{
		{AudioFile: catalog.AudioFile{DurationMs: 600000}},
		{AudioFile: catalog.AudioFile{DurationMs: 540000}},
	}
}

func TestIsOneChapterPerFileNoChapters(t *testing.T) {
	// No chapters is not the degenerate one-per-file shape — it is "none".
	if isOneChapterPerFile(nil, twoFileProbes()) {
		t.Fatal("no chapters is not one-chapter-per-file")
	}
}

func TestIsOneChapterPerFileTrue(t *testing.T) {
	// flattenBookChapters output for two chapterless files: one chapter spanning
	// each whole file. Navigationally useless → weak "file" provenance.
	chapters := []catalog.AudioChapter{
		{Index: 1, StartSeconds: 0, EndSeconds: 600},
		{Index: 2, StartSeconds: 600, EndSeconds: 1140},
	}
	if !isOneChapterPerFile(chapters, twoFileProbes()) {
		t.Fatal("one-chapter-per-file should be detected")
	}
}

func TestIsOneChapterPerFileRealChapters(t *testing.T) {
	// Real intra-file chapters (more chapters than files, boundaries off the file
	// edges) are NOT the degenerate shape.
	chapters := []catalog.AudioChapter{
		{Index: 1, StartSeconds: 0, EndSeconds: 300},
		{Index: 2, StartSeconds: 300, EndSeconds: 600},
		{Index: 3, StartSeconds: 600, EndSeconds: 900},
		{Index: 4, StartSeconds: 900, EndSeconds: 1140},
	}
	if isOneChapterPerFile(chapters, twoFileProbes()) {
		t.Fatal("real chapters are not one-chapter-per-file")
	}
}

func TestIsOneChapterPerFileSingleFile(t *testing.T) {
	probes := []probedFile{{AudioFile: catalog.AudioFile{DurationMs: 600000}}}
	chapters := []catalog.AudioChapter{{Index: 1, StartSeconds: 0, EndSeconds: 600}}
	if isOneChapterPerFile(chapters, probes) {
		t.Fatal("single-file single-chapter book is not one-chapter-per-file")
	}
}
