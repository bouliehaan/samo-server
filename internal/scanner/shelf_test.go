package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGroupAudiobooksUsesAuthorBookFolder(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "library")
	files := []string{
		filepath.Join(root, "Ada Archive", "Signal Manual", "part1.mp3"),
		filepath.Join(root, "Ada Archive", "Signal Manual", "part2.mp3"),
		filepath.Join(root, "Other Author", "Other Book", "part1.mp3"),
	}

	groups := groupAudiobooks(root, files)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if got := filepath.Base(groups[0].Root); got != "Signal Manual" {
		t.Fatalf("first group = %q, want Signal Manual", got)
	}
}

func TestGroupAudiobooksHandlesDiscSubfolders(t *testing.T) {
	root := t.TempDir()
	bookRoot := filepath.Join(root, "BookWithDiscs")
	disc1 := filepath.Join(bookRoot, "Disc 1")
	disc2 := filepath.Join(bookRoot, "Disc 2")
	mustMkdir(t, disc1)
	mustMkdir(t, disc2)
	track1 := writeFile(t, disc1, "01.mp3", "")
	track2 := writeFile(t, disc1, "02.mp3", "")
	track3 := writeFile(t, disc2, "01.mp3", "")

	groups := groupAudiobooks(root, []string{track1, track2, track3})
	if len(groups) != 1 {
		t.Fatalf("audiobook group count = %d, want 1", len(groups))
	}
	if groups[0].Root != bookRoot {
		t.Fatalf("audiobook root = %q, want %q", groups[0].Root, bookRoot)
	}
	if len(groups[0].Files) != 3 {
		t.Fatalf("audiobook files = %v", groups[0].Files)
	}
}

func TestGroupAudiobooksKeepsAuthorBookWithDiscs(t *testing.T) {
	root := t.TempDir()
	bookOne := filepath.Join(root, "Author", "Book One")
	bookTwo := filepath.Join(root, "Author", "Book Two")
	disc1 := filepath.Join(bookOne, "Disc 1")
	disc2 := filepath.Join(bookTwo, "Part 1")
	mustMkdir(t, disc1)
	mustMkdir(t, disc2)
	a := writeFile(t, disc1, "01.mp3", "")
	b := writeFile(t, disc2, "01.mp3", "")

	groups := groupAudiobooks(root, []string{a, b})
	if len(groups) != 2 {
		t.Fatalf("audiobook group count = %d, want 2", len(groups))
	}
}

func TestGroupAudiobooksKeepsSeriesBooksSeparate(t *testing.T) {
	root := t.TempDir()
	bookOne := filepath.Join(root, "J.K. Rowling", "Harry Potter", "Book One")
	bookTwo := filepath.Join(root, "J.K. Rowling", "Harry Potter", "Book Two")
	mustMkdir(t, bookOne)
	mustMkdir(t, bookTwo)
	a := writeFile(t, bookOne, "01.mp3", "")
	b := writeFile(t, bookTwo, "01.mp3", "")

	groups := groupAudiobooks(root, []string{a, b})
	if len(groups) != 2 {
		t.Fatalf("audiobook group count = %d, want 2", len(groups))
	}
}

func TestGroupAudiobooksGroupsChapterMP3sTogether(t *testing.T) {
	root := t.TempDir()
	bookRoot := filepath.Join(root, "Harry Potter and the Stone")
	mustMkdir(t, bookRoot)
	files := []string{
		writeFile(t, bookRoot, "01 - Chapter One.mp3", ""),
		writeFile(t, bookRoot, "02 - Chapter Two.mp3", ""),
		writeFile(t, bookRoot, "03 - Chapter Three.mp3", ""),
	}

	groups := groupAudiobooks(root, files)
	if len(groups) != 1 {
		t.Fatalf("audiobook group count = %d, want 1", len(groups))
	}
	if groups[0].Root != bookRoot {
		t.Fatalf("audiobook root = %q, want %q", groups[0].Root, bookRoot)
	}
	if len(groups[0].Files) != 3 {
		t.Fatalf("audiobook files = %d, want 3", len(groups[0].Files))
	}
}

func TestGroupAudiobooksKeepsNumericBookFoldersSeparate(t *testing.T) {
	root := t.TempDir()
	bookOne := filepath.Join(root, "Harry Potter", "01")
	bookTwo := filepath.Join(root, "Harry Potter", "02")
	mustMkdir(t, bookOne)
	mustMkdir(t, bookTwo)
	a := writeFile(t, bookOne, "01.mp3", "")
	b := writeFile(t, bookTwo, "01.mp3", "")

	groups := groupAudiobooks(root, []string{a, b})
	if len(groups) != 2 {
		t.Fatalf("audiobook group count = %d, want 2", len(groups))
	}
}

func TestGroupAudiobooksKeepsFlatRootFilesSeparate(t *testing.T) {
	root := t.TempDir()
	a := writeFile(t, root, "Book One.mp3", "")
	b := writeFile(t, root, "Book Two.m4b", "")

	groups := groupAudiobooks(root, []string{a, b})
	if len(groups) != 2 {
		t.Fatalf("audiobook group count = %d, want 2", len(groups))
	}
	if groups[0].Root == root || groups[1].Root == root {
		t.Fatalf("flat root files grouped into library root: %#v", groups)
	}
}

func TestGroupAudiobooksKeepsAuthorSingleFileBooksSeparate(t *testing.T) {
	root := t.TempDir()
	author := filepath.Join(root, "Neil Gaiman")
	mustMkdir(t, author)
	a := writeFile(t, author, "American Gods.mp3", "")
	b := writeFile(t, author, "Good Omens.mp3", "")

	groups := groupAudiobooks(root, []string{a, b})
	if len(groups) != 2 {
		t.Fatalf("audiobook group count = %d, want 2", len(groups))
	}
	if groups[0].Root == author || groups[1].Root == author {
		t.Fatalf("single-file books grouped into author folder: %#v", groups)
	}
}

func TestBookSidecarReadsOPFDescAndReader(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "desc.txt"), []byte("Sidecar description"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reader.txt"), []byte("Nora Noise"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "book.opf"), []byte(`<?xml version="1.0"?>
<package>
  <metadata>
    <title>Signal Manual</title>
    <creator>Speaker One</creator>
    <publisher>Samo Press</publisher>
    <date>2026</date>
    <subject>Science Fiction</subject>
    <language>en</language>
    <identifier scheme="ISBN">9780000000001</identifier>
    <meta name="calibre:series" content="Signals"/>
    <meta name="calibre:series_index" content="1"/>
  </metadata>
</package>`), 0o644); err != nil {
		t.Fatal(err)
	}

	sidecar := readBookSidecar(dir)
	if sidecar.Title != "Signal Manual" {
		t.Fatalf("title = %q, want Signal Manual", sidecar.Title)
	}
	if sidecar.Description != "Sidecar description" {
		t.Fatalf("description = %q, want desc.txt", sidecar.Description)
	}
	if got := sidecar.Narrators[0]; got != "Nora Noise" {
		t.Fatalf("narrator = %q, want Nora Noise", got)
	}
	if len(sidecar.Series) != 1 || sidecar.Series[0].Sequence != 1 {
		t.Fatalf("series = %#v, want Signals #1", sidecar.Series)
	}
}
