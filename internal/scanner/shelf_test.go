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
