package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

// A multi-disc release keeps its artwork at the album root and splits only the
// audio into CD1/CD2. Cover lookup used to read the audio file's own folder and
// nothing else, so every one of those albums came out with no artwork at all.
func TestFindDiscParentCoverImage(t *testing.T) {
	root := t.TempDir()

	albumDir := filepath.Join(root, "deadmau5", "For Lack of a Better Name")
	discDir := filepath.Join(albumDir, "CD1")
	if err := os.MkdirAll(discDir, 0o755); err != nil {
		t.Fatal(err)
	}
	albumArt := filepath.Join(albumDir, "cover.jpg")
	if err := os.WriteFile(albumArt, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An image at the library root must never be adopted as an album cover.
	if err := os.WriteFile(filepath.Join(root, "cover.jpg"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	cover := findDiscParentCoverImage(root, discDir)
	if cover == nil {
		t.Fatal("no cover found for a disc subfolder, want the album folder's cover.jpg")
	}
	if cover.Path != albumArt {
		t.Fatalf("cover path = %q, want %q", cover.Path, albumArt)
	}
	if cover.MimeType != "image/jpeg" {
		t.Fatalf("cover mime = %q, want image/jpeg", cover.MimeType)
	}
}

// The walk is one level and only out of a disc folder: an ordinary album folder
// must not inherit the artist folder's photo, and nothing may reach the root.
func TestFindDiscParentCoverImageStaysPut(t *testing.T) {
	root := t.TempDir()

	artistDir := filepath.Join(root, "deadmau5")
	albumDir := filepath.Join(artistDir, "For Lack of a Better Name")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artistDir, "folder.jpg"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	if cover := findDiscParentCoverImage(root, albumDir); cover != nil {
		t.Fatalf("album folder adopted %q from the artist folder, want no cover", cover.Path)
	}

	// A disc folder sitting directly under the library root has no album folder
	// to inherit from, and must not take the root's image.
	discAtRoot := filepath.Join(root, "CD1")
	if err := os.MkdirAll(discAtRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cover.jpg"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cover := findDiscParentCoverImage(root, discAtRoot); cover != nil {
		t.Fatalf("disc folder adopted %q from the library root, want no cover", cover.Path)
	}
}

func TestDiscFolderPattern(t *testing.T) {
	for _, name := range []string{"CD1", "cd 1", "Disc 2", "disc02", "Disk 1", "Side A", "Vol 3", "CD", "volume 1"} {
		if !discFolderPattern.MatchString(name) {
			t.Errorf("%q not recognised as a disc folder", name)
		}
	}
	for _, name := range []string{"For Lack of a Better Name", "Bonus Tracks", "Discography", "Volumes of Noise", "Cider"} {
		if discFolderPattern.MatchString(name) {
			t.Errorf("%q wrongly treated as a disc folder", name)
		}
	}
}

// Artwork at the album root must be found for audio in a disc subfolder no
// matter what the file is called. The disc-parent lookup used to probe only the
// cover/folder/front/artwork/album stems, so a real-world "AlbumArt.jpg" or a
// scan named after the record produced an album with no art — and renaming that
// exact file to folder.jpg "fixed" it, which is what made this look like a
// filename rule rather than a bug.
func TestFindDiscParentCoverImageAnyFilename(t *testing.T) {
	for _, name := range []string{
		"folder.jpg", "cover.jpg", "front.jpg", "artwork.png",
		"AlbumArt.jpg", "Blonde On Blonde.jpg", "scan01.jpg", "artwork_large.jpg",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			albumDir := filepath.Join(root, "Artist", "Album")
			discDir := filepath.Join(albumDir, "CD1")
			if err := os.MkdirAll(discDir, 0o755); err != nil {
				t.Fatal(err)
			}
			art := filepath.Join(albumDir, name)
			if err := os.WriteFile(art, []byte("img"), 0o644); err != nil {
				t.Fatal(err)
			}
			cover := findDiscParentCoverImage(root, discDir)
			if cover == nil {
				t.Fatalf("no cover found for %q at the album root", name)
			}
			if cover.Path != art {
				t.Fatalf("cover path = %q, want %q", cover.Path, art)
			}
		})
	}
}
