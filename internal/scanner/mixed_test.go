package scanner

import (
	"fmt"
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

func TestSplitMixedGroupsKeepsAuthorBookSeparated(t *testing.T) {
	root := t.TempDir()
	bookOne := filepath.Join(root, "Author", "Book One")
	bookTwo := filepath.Join(root, "Author", "Book Two")
	mustMkdir(t, bookOne)
	mustMkdir(t, bookTwo)
	writeFile(t, bookOne, "metadata.json", "{}")
	writeFile(t, bookTwo, "metadata.json", "{}")
	disc1 := filepath.Join(bookOne, "Disc 1")
	disc2 := filepath.Join(bookTwo, "Disc 1")
	mustMkdir(t, disc1)
	mustMkdir(t, disc2)
	a := writeFile(t, disc1, "01.mp3", "")
	b := writeFile(t, disc2, "01.mp3", "")

	groups := splitMixedGroups(root, []string{a, b})
	if len(groups.audiobooks) != 2 {
		t.Fatalf("audiobook group count = %d, want 2", len(groups.audiobooks))
	}
}

func TestSplitMixedGroupsDetectsChapterMP3Audiobooks(t *testing.T) {
	root := t.TempDir()
	bookRoot := filepath.Join(root, "Harry Potter and the Stone")
	mustMkdir(t, bookRoot)
	files := []string{
		writeFile(t, bookRoot, "01 - Chapter One.mp3", ""),
		writeFile(t, bookRoot, "02 - Chapter Two.mp3", ""),
		writeFile(t, bookRoot, "03 - Chapter Three.mp3", ""),
	}

	groups := splitMixedGroups(root, files)
	if len(groups.audiobooks) != 1 {
		t.Fatalf("audiobook group count = %d, want 1", len(groups.audiobooks))
	}
	if groups.audiobooks[0].Root != bookRoot {
		t.Fatalf("audiobook root = %q, want %q", groups.audiobooks[0].Root, bookRoot)
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

// A fresh install's mixed library, laid out the way rippers and taggers name
// things: every album must stay music. "01 - Title" and "01 Title" used to
// read as audiobook chapters, so a newbie who picked Mixed in the setup wizard
// got their whole music collection filed under Audiobooks.
func TestSplitMixedGroupsKeepsNumberedAlbumsAsMusic(t *testing.T) {
	root := t.TempDir()
	ripped := filepath.Join(root, "Music", "Test Tones", "Sine Waves (2021)")
	beets := filepath.Join(root, "Music", "Fractal Orchestra", "Mandelbrot Suite")
	book := filepath.Join(root, "Audiobooks", "Ada Author", "The Long Tone")
	for _, dir := range []string{ripped, beets, book} {
		mustMkdir(t, dir)
	}
	var files []string
	for i := 1; i <= 6; i++ {
		files = append(files, writeFile(t, ripped, fmt.Sprintf("%02d - Tone %d.flac", i, i), ""))
	}
	for i := 1; i <= 12; i++ {
		files = append(files, writeFile(t, beets, fmt.Sprintf("%02d Movement %d.mp3", i, i), ""))
	}
	files = append(files, writeFile(t, book, "The Long Tone.m4b", ""))

	groups := splitMixedGroups(root, files)
	if len(groups.music) != 18 {
		t.Fatalf("music files = %d, want 18 (audiobooks %v, podcasts %v)", len(groups.music), groups.audiobooks, groups.podcasts)
	}
	if len(groups.audiobooks) != 1 || len(groups.podcasts) != 0 {
		t.Fatalf("audiobooks = %v, podcasts = %v; want the one m4b and no shows", groups.audiobooks, groups.podcasts)
	}
}

// "Artist - Title" looks exactly like "Show - Episode". The tags tell them
// apart: an album and a track number is music.
func TestSplitMixedGroupsTaggedSinglesStayMusic(t *testing.T) {
	root := t.TempDir()
	singles := filepath.Join(root, "Singles")
	mustMkdir(t, singles)
	var files []string
	for i := 1; i <= 10; i++ {
		files = append(files, writeID3(t, singles, fmt.Sprintf("Colour Bars - Song %d.mp3", i), map[string]string{
			"TPE1": "Colour Bars", "TALB": "Calibration", "TRCK": fmt.Sprintf("%d/10", i), "TCON": "Ambient",
		}))
	}
	groups := splitMixedGroups(root, files)
	if len(groups.music) != 10 {
		t.Fatalf("music = %d, audiobooks = %v, podcasts = %v; want all 10 as music", len(groups.music), groups.audiobooks, groups.podcasts)
	}
}

// A CD-ripped audiobook has nothing in its filenames to say so; its genre tag
// does.
func TestSplitMixedGroupsUsesSpokenWordGenre(t *testing.T) {
	root := t.TempDir()
	disc := filepath.Join(root, "Some Novel")
	mustMkdir(t, disc)
	var files []string
	for i := 1; i <= 5; i++ {
		files = append(files, writeID3(t, disc, fmt.Sprintf("%02d Track %d.mp3", i, i), map[string]string{
			"TALB": "Some Novel", "TRCK": fmt.Sprint(i), "TCON": "Audiobook",
		}))
	}
	groups := splitMixedGroups(root, files)
	if len(groups.audiobooks) != 1 || len(groups.music) != 0 {
		t.Fatalf("audiobooks = %v, music = %v; want one book", groups.audiobooks, groups.music)
	}
}

func TestSplitMixedGroupsDetectsEpisodeNaming(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Some Show")
	mustMkdir(t, show)
	files := []string{
		writeFile(t, show, "Episode 1 - Pilot.mp3", ""),
		writeFile(t, show, "Episode 2 - Second.mp3", ""),
		writeFile(t, show, "Episode 3 - Third.mp3", ""),
	}
	groups := splitMixedGroups(root, files)
	if len(groups.podcasts) != 1 {
		t.Fatalf("podcasts = %v, want one show", groups.podcasts)
	}
}

// writeID3 writes a file that is nothing but an ID3v2.3 tag holding the given
// text frames — enough for the tag reader, with no audio behind it.
func writeID3(t *testing.T, dir, name string, frames map[string]string) string {
	t.Helper()
	var body []byte
	for id, text := range frames {
		data := append([]byte{0}, text...) // ISO-8859-1
		size := len(data)
		body = append(body, id...)
		body = append(body, byte(size>>24), byte(size>>16), byte(size>>8), byte(size), 0, 0)
		body = append(body, data...)
	}
	size := len(body)
	header := []byte{'I', 'D', '3', 3, 0, 0,
		byte(size >> 21 & 0x7f), byte(size >> 14 & 0x7f), byte(size >> 7 & 0x7f), byte(size & 0x7f)}
	return writeFile(t, dir, name, string(append(header, body...)))
}
