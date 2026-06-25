package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFilterFilesUnderSubpaths(t *testing.T) {
	files := []string{
		"/music/Artist A/Album 1/track.flac",
		"/music/Artist A/Album 2/track.flac",
		"/music/Artist B/Album 1/track.flac",
	}
	filtered := filterFilesUnderSubpaths(files, []string{"/music/Artist A/Album 1"})
	if len(filtered) != 1 || filtered[0] != files[0] {
		t.Fatalf("filtered = %#v, want only album 1 track", filtered)
	}
}

func TestCountAudioFilesInSubpaths(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	albumDir := filepath.Join(root, "Artist", "Album")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.flac", "two.flac"} {
		if err := os.WriteFile(filepath.Join(albumDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "Other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Other", "skip.flac"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := CountAudioFilesInSubpaths(ctx, root, []string{albumDir})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestResolveIncrementalScanRoot(t *testing.T) {
	root := t.TempDir()
	track := filepath.Join(root, "Artist", "Album", "song.flac")
	if err := os.MkdirAll(filepath.Dir(track), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(track, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveIncrementalScanRoot(track)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "Artist", "Album")
	if got != want {
		t.Fatalf("scan root = %q, want %q", got, want)
	}
}
