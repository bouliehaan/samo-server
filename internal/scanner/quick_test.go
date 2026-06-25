package scanner

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileNeedsProbe(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "song.flac")
	if err := os.WriteFile(path, []byte("same-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	checksum := fileChecksum(path, stat)

	scanner := &Scanner{
		scanMode: ScanModeQuick,
		fileIndex: map[string]indexedFile{
			path: {Checksum: checksum},
		},
	}
	if scanner.fileNeedsProbe(path) {
		t.Fatal("expected unchanged file to skip probing")
	}
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !scanner.fileNeedsProbe(path) {
		t.Fatal("expected changed file to require probing")
	}
}

func TestSkipUnchangedFileMarksPruneTargets(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "book.m4b")
	if err := os.WriteFile(path, []byte("part-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	scanner := &Scanner{
		scanMode: ScanModeQuick,
		fileIndex: map[string]indexedFile{
			path: {
				Checksum:    fileChecksum(path, stat),
				AudiobookID: "audiobook_test",
			},
		},
		activeScan: newScanAccumulator(),
	}
	if !scanner.skipUnchangedFile(path) {
		t.Fatal("expected unchanged file to be skipped")
	}
	if len(scanner.activeScan.filePaths) != 1 {
		t.Fatalf("filePaths = %d, want 1", len(scanner.activeScan.filePaths))
	}
	if len(scanner.activeScan.audiobookIDs) != 1 {
		t.Fatalf("audiobookIDs = %d, want 1", len(scanner.activeScan.audiobookIDs))
	}
}

func TestGroupNeedsProbeWhenAnyFileChanged(t *testing.T) {
	root := t.TempDir()
	unchangedPath := filepath.Join(root, "a.m4b")
	changedPath := filepath.Join(root, "b.m4b")
	for _, target := range []struct {
		path    string
		content []byte
	}{
		{unchangedPath, []byte("a")},
		{changedPath, []byte("b")},
	} {
		if err := os.WriteFile(target.path, target.content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	unchangedStat, err := os.Stat(unchangedPath)
	if err != nil {
		t.Fatal(err)
	}

	scanner := &Scanner{
		scanMode: ScanModeQuick,
		fileIndex: map[string]indexedFile{
			unchangedPath: {Checksum: fileChecksum(unchangedPath, unchangedStat)},
			changedPath:   {Checksum: "stale-checksum"},
		},
	}
	if !scanner.groupNeedsProbe([]string{unchangedPath, changedPath}) {
		t.Fatal("expected group probe when any member changed")
	}
}

func TestNormalizeScanMode(t *testing.T) {
	if normalizeScanMode("") != ScanModeFull {
		t.Fatalf("empty mode = %q, want full", normalizeScanMode(""))
	}
	if normalizeScanMode("QUICK") != ScanModeQuick {
		t.Fatalf("quick mode = %q, want quick", normalizeScanMode("QUICK"))
	}
}

func TestFileChecksumUsesMtime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "track.mp3")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	firstSum := fileChecksum(path, first)

	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	updated, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileChecksum(path, updated) == firstSum {
		t.Fatal("expected checksum to change when mtime changes")
	}
}
