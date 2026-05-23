package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadJSONBookMetadata(t *testing.T) {
	dir := t.TempDir()
	payload := `{
	  "title": "Signal Manual",
	  "authors": ["Speaker One"],
	  "narrators": ["Nora Noise"],
	  "series": [{"name": "Signals", "sequence": 2}]
	}`
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	sidecar := readBookSidecar(dir)
	if sidecar.Title != "Signal Manual" {
		t.Fatalf("title = %q", sidecar.Title)
	}
	if sidecar.Narrators[0] != "Nora Noise" {
		t.Fatalf("narrators = %#v", sidecar.Narrators)
	}
	if len(sidecar.Series) != 1 || sidecar.Series[0].Sequence != 2 {
		t.Fatalf("series = %#v", sidecar.Series)
	}
}

func TestParseCueFileBuildsChapters(t *testing.T) {
	dir := t.TempDir()
	cue := `FILE "part1.mp3" MP3
  TRACK 01 AUDIO
    TITLE "Opening"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Chapter Two"
    INDEX 01 00:10:00
`
	path := filepath.Join(dir, "book.cue")
	if err := os.WriteFile(path, []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}

	chapters := parseCueFile(path)
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(chapters))
	}
	if chapters[0].Title != "Opening" || chapters[0].StartSeconds != 0 {
		t.Fatalf("first chapter = %#v", chapters[0])
	}
	if chapters[0].EndSeconds != 600 {
		t.Fatalf("first chapter end = %d, want 600", chapters[0].EndSeconds)
	}
}

func TestReadMusicAlbumSidecarFromNFO(t *testing.T) {
	dir := t.TempDir()
	nfo := `<album>
  <title>Night Broadcasts</title>
  <albumartist>The Static</albumartist>
  <year>2024</year>
  <genre>Ambient</genre>
  <uniqueid type="upc">012345678901</uniqueid>
</album>`
	if err := os.WriteFile(filepath.Join(dir, "album.nfo"), []byte(nfo), 0o644); err != nil {
		t.Fatal(err)
	}

	sidecar := readMusicAlbumSidecar(dir)
	if sidecar.Title != "Night Broadcasts" {
		t.Fatalf("title = %q", sidecar.Title)
	}
	if sidecar.Barcode != "012345678901" {
		t.Fatalf("barcode = %q", sidecar.Barcode)
	}
}
