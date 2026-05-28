package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWalkLibraryDirFindsAudioFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	album := filepath.Join(root, "Artist", "Album")
	if err := os.MkdirAll(album, 0o755); err != nil {
		t.Fatal(err)
	}
	track := filepath.Join(album, "song.flac")
	if err := os.WriteFile(track, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden", "skip.flac"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var found []string
	err := walkLibraryDir(ctx, root, func(path string, _ os.DirEntry) error {
		if isAudioPath(path) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0] != track {
		t.Fatalf("found = %#v, want %q", found, track)
	}
}
