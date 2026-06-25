package scanner

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The scope contract that keeps reboots and routine scans cheap: version
// migration is an explicit full-scan event, never a side effect of booting or
// of a quick scan over an unchanged library.
func TestChapterAnalysisEligibility(t *testing.T) {
	files := []catalog.AudioFile{{Path: "/books/b/1.mp3", SizeBytes: 100, Checksum: "abc"}}
	cur := audioChapterSignature(files)           // "v3:<hash>"
	oldVer := "v2:" + audioChapterFileHash(files) // same files, older analyzer
	changed := []catalog.AudioFile{{Path: "/books/b/1.mp3", SizeBytes: 100, Checksum: "DIFFERENT"}}

	const strong = chapterSourceAudioAligned // an authoritative result
	cases := []struct {
		name   string
		scope  ChapterPassScope
		sig    string
		source string
		files  []catalog.AudioFile
		want   bool
		reason string
	}{
		{"new book, quick scan", ChapterPassChanged, "", "", files, true,
			"never-analyzed books are eligible everywhere"},
		{"unchanged + current version, quick", ChapterPassChanged, cur, strong, files, false,
			"nothing changed — must not decode"},
		{"unchanged + current version + good result, migrate", ChapterPassMigrate, cur, strong, files, false,
			"already on this analyzer version with an authoritative result — full scan must not re-decode it"},
		{"files changed, quick", ChapterPassChanged, cur, strong, changed, true,
			"changed audio re-analyzes on any pass"},
		{"old analyzer version, quick", ChapterPassChanged, oldVer, strong, files, false,
			"version upgrades wait for a full scan — THE reboot/quick-scan guarantee"},
		{"old analyzer version, migrate", ChapterPassMigrate, oldVer, strong, files, true,
			"full scan migrates old-version books"},
		{"pre-split opaque sig, quick", ChapterPassChanged, "a1b2c3d4e5f60718", chapterSourceFile, files, false,
			"legacy sigs are version-stale only; quick scans leave them alone"},
		{"pre-split opaque sig, migrate", ChapterPassMigrate, "a1b2c3d4e5f60718", chapterSourceFile, files, true,
			"full scan migrates legacy-sig books"},
		{"weak file result, quick", ChapterPassChanged, cur, chapterSourceFile, files, false,
			"a weak result is still not worth a heavy re-decode on a routine quick scan"},
		{"weak file result, migrate", ChapterPassMigrate, cur, chapterSourceFile, files, true,
			"THE fix: a full scan retries a book stuck on one-chapter-per-file so improved metadata/Audnexus can rescue it"},
		{"unverified audio guess, migrate", ChapterPassMigrate, cur, chapterSourceAudioDetected, files, true,
			"an unverified audio guess is weak — a full scan retries it"},
		{"verified Audnexus result, migrate", ChapterPassMigrate, cur, ChapterSourceAudnexus, files, false,
			"a verified edition is authoritative — full scan leaves it alone"},
		{"embedded markers, old version migrate", ChapterPassMigrate, oldVer, chapterSourceEmbedded, files, false,
			"real in-file markers are Audible's own — never decode them, not even to migrate"},
		{"embedded markers, files changed", ChapterPassChanged, cur, chapterSourceEmbedded, changed, false,
			"embedded stays embedded; the heavy pass that pegs the box must not touch it"},
		{"cue markers, force", ChapterPassForce, cur, chapterSourceCue, files, false,
			"even --all skips authoritative in-file markers"},
		{"weak file result, force", ChapterPassForce, cur, chapterSourceFile, files, true,
			"--all still re-runs a marker-less rip"},
	}
	for _, tc := range cases {
		if got := chapterAnalysisEligible(tc.scope, tc.sig, tc.source, tc.files); got != tc.want {
			t.Errorf("%s: eligible=%v, want %v (%s)", tc.name, got, tc.want, tc.reason)
		}
	}
}

// Old-version books whose FILES also changed re-analyze on a quick scan (the
// change makes the old result describe audio that no longer exists), and pick
// up the new version's signature in the process.
func TestChapterAnalysisEligibleVersionBumpPiggybacksOnFileChange(t *testing.T) {
	files := []catalog.AudioFile{{Path: "/books/b/1.mp3", SizeBytes: 100, Checksum: "abc"}}
	if !chapterAnalysisEligible(ChapterPassChanged, "v2:0123456789abcdef", chapterSourceAudioAligned, files) {
		t.Fatalf("old version + changed files must be eligible on a quick scan")
	}
}
