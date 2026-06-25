package watch

import (
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestInterestingPathIncludesAudioMetadataAndCovers(t *testing.T) {
	tests := []string{
		"/library/album/song.flac",
		"/library/book/book.opf",
		"/library/book/desc.txt",
		"/library/book/reader.txt",
		"/library/book/cover.jpg",
	}

	for _, path := range tests {
		if !isInterestingPath(path) {
			t.Fatalf("path %q should be interesting", path)
		}
	}
}

func TestInterestingEventIgnoresPlainChmod(t *testing.T) {
	event := fsnotify.Event{Name: "/library/album/song.flac", Op: fsnotify.Chmod}
	if interestingEvent(event) {
		t.Fatal("plain chmod event should not trigger scans")
	}
}

func TestInterestingPathIgnoresUnrelatedFiles(t *testing.T) {
	if isInterestingPath("/library/album/notes.tmp") {
		t.Fatal("temporary note file should not be interesting")
	}
}
