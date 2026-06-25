package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAudioFilesIgnoreWalkStacked(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".ndignore"), []byte("skip/**\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skipDir := filepath.Join(root, "skip", "nested")
	if err := os.MkdirAll(skipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skipDir, "hidden.flac"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	keepDir := filepath.Join(root, "keep")
	if err := os.MkdirAll(keepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keepDir, "song.flac"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := audioFiles(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "song.flac" {
		t.Fatalf("files = %#v, want only keep/song.flac", files)
	}
}

func TestAudioFilesDeepTreeDoesNotRepushRoot(t *testing.T) {
	root := t.TempDir()
	current := root
	for i := 0; i < 40; i++ {
		current = filepath.Join(current, "d")
		if err := os.Mkdir(current, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(current, "track.flac"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := audioFiles(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
}
