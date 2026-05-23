package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitMixedGroupsRoutesAudiobookSidecars(t *testing.T) {
	root := t.TempDir()

	// Music album: two tracks in an artist/album folder, no sidecars.
	musicFolder := filepath.Join(root, "Artist", "Album")
	mustMkdir(t, musicFolder)
	musicA := writeFile(t, musicFolder, "01-track.mp3", "")
	musicB := writeFile(t, musicFolder, "02-track.mp3", "")

	// Audiobook with metadata.json sidecar.
	bookFolder := filepath.Join(root, "BookOne")
	mustMkdir(t, bookFolder)
	writeFile(t, bookFolder, "metadata.json", "{}")
	bookA := writeFile(t, bookFolder, "part-01.mp3", "")
	bookB := writeFile(t, bookFolder, "part-02.mp3", "")

	// Audiobook signalled by .m4b extension only.
	m4bFolder := filepath.Join(root, "BookTwo")
	mustMkdir(t, m4bFolder)
	m4bFile := writeFile(t, m4bFolder, "Book Two.m4b", "")

	// Loose file in root → music.
	rootTrack := writeFile(t, root, "loose-track.mp3", "")

	files := []string{musicA, musicB, bookA, bookB, m4bFile, rootTrack}
	groups := splitMixedGroups(root, files)

	if len(groups.music) != 3 {
		t.Fatalf("music count = %d (%v), want 3", len(groups.music), groups.music)
	}
	if len(groups.audiobooks) != 2 {
		t.Fatalf("audiobook group count = %d, want 2 (got %#v)", len(groups.audiobooks), groups.audiobooks)
	}

	books := map[string][]string{}
	for _, group := range groups.audiobooks {
		books[group.Root] = group.Files
	}
	if got := books[bookFolder]; len(got) != 2 {
		t.Errorf("book one files = %v", got)
	}
	if got := books[m4bFolder]; len(got) != 1 {
		t.Errorf("book two files = %v", got)
	}
}

func TestSplitMixedGroupsHandlesDiscSubfolders(t *testing.T) {
	root := t.TempDir()
	bookRoot := filepath.Join(root, "BookWithDiscs")
	mustMkdir(t, bookRoot)
	writeFile(t, bookRoot, "metadata.json", "{}")
	disc1 := filepath.Join(bookRoot, "Disc 1")
	disc2 := filepath.Join(bookRoot, "Disc 2")
	mustMkdir(t, disc1)
	mustMkdir(t, disc2)
	track1 := writeFile(t, disc1, "01.mp3", "")
	track2 := writeFile(t, disc1, "02.mp3", "")
	track3 := writeFile(t, disc2, "01.mp3", "")

	groups := splitMixedGroups(root, []string{track1, track2, track3})
	if len(groups.audiobooks) != 1 {
		t.Fatalf("audiobook group count = %d, want 1", len(groups.audiobooks))
	}
	if groups.audiobooks[0].Root != bookRoot {
		t.Fatalf("audiobook root = %q, want %q", groups.audiobooks[0].Root, bookRoot)
	}
	if len(groups.audiobooks[0].Files) != 3 {
		t.Fatalf("audiobook files = %v", groups.audiobooks[0].Files)
	}
}

func TestSplitMixedGroupsDefaultsToMusic(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Artist", "Album")
	mustMkdir(t, folder)
	a := writeFile(t, folder, "01.mp3", "")
	b := writeFile(t, folder, "02.mp3", "")
	c := writeFile(t, folder, "03.mp3", "")
	groups := splitMixedGroups(root, []string{a, b, c})
	if len(groups.audiobooks) != 0 {
		t.Fatalf("audiobook groups = %d, want 0", len(groups.audiobooks))
	}
	if len(groups.music) != 3 {
		t.Fatalf("music files = %d, want 3", len(groups.music))
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	return path
}
