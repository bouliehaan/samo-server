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

	cases := []struct {
		name   string
		scope  ChapterPassScope
		sig    string
		files  []catalog.AudioFile
		want   bool
		reason string
	}{
		{"new book, quick scan", ChapterPassChanged, "", files, true,
			"never-analyzed books are eligible everywhere"},
		{"unchanged + current version, quick", ChapterPassChanged, cur, files, false,
			"nothing changed — must not decode"},
		{"unchanged + current version, migrate", ChapterPassMigrate, cur, files, false,
			"already on this analyzer version — full scan must not re-decode it either"},
		{"files changed, quick", ChapterPassChanged, cur, changed, true,
			"changed audio re-analyzes on any pass"},
		{"old analyzer version, quick", ChapterPassChanged, oldVer, files, false,
			"version upgrades wait for a full scan — THE reboot/quick-scan guarantee"},
		{"old analyzer version, migrate", ChapterPassMigrate, oldVer, files, true,
			"full scan migrates old-version books"},
		{"pre-split opaque sig, quick", ChapterPassChanged, "a1b2c3d4e5f60718", files, false,
			"legacy sigs are version-stale only; quick scans leave them alone"},
		{"pre-split opaque sig, migrate", ChapterPassMigrate, "a1b2c3d4e5f60718", files, true,
			"full scan migrates legacy-sig books"},
		{"anything, force", ChapterPassForce, cur, files, true,
			"--all overrides every signature"},
	}
	for _, tc := range cases {
		if got := chapterAnalysisEligible(tc.scope, tc.sig, tc.files); got != tc.want {
			t.Errorf("%s: eligible=%v, want %v (%s)", tc.name, got, tc.want, tc.reason)
		}
	}
}

// Old-version books whose FILES also changed re-analyze on a quick scan (the
// change makes the old result describe audio that no longer exists), and pick
// up the new version's signature in the process.
func TestChapterAnalysisEligibleVersionBumpPiggybacksOnFileChange(t *testing.T) {
	files := []catalog.AudioFile{{Path: "/books/b/1.mp3", SizeBytes: 100, Checksum: "abc"}}
	if !chapterAnalysisEligible(ChapterPassChanged, "v2:0123456789abcdef", files) {
		t.Fatalf("old version + changed files must be eligible on a quick scan")
	}
}
