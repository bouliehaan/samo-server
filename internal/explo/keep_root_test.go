package explo

import (
	"os"
	"path/filepath"
	"testing"
)

// A mixed library laid out the usual way keeps into its Music folder, whatever
// its case; one without a Music folder keeps into the root.
func TestMusicSubfolderOfAMixedLibrary(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"Audiobooks", "music", "Podcasts"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := musicSubfolder(root), filepath.Join(root, "music"); got != want {
		t.Fatalf("musicSubfolder = %q, want %q", got, want)
	}
	flat := t.TempDir()
	if err := os.WriteFile(filepath.Join(flat, "Music"), nil, 0o644); err != nil {
		t.Fatal(err) // a file called Music is not a folder to keep into
	}
	if got := musicSubfolder(flat); got != "" {
		t.Fatalf("musicSubfolder of a library without a Music folder = %q, want \"\"", got)
	}
}
