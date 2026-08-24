package watch

import (
	"testing"
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

func TestInterestingPathIgnoresUnrelatedFiles(t *testing.T) {
	if isInterestingPath("/library/album/notes.tmp") {
		t.Fatal("temporary note file should not be interesting")
	}
}

func TestRootsChangedDetectsAddedRenamedAndRepathedLibraries(t *testing.T) {
	base := []LibraryRoot{{ID: "a", Path: "/srv/music"}, {ID: "b", Path: "/srv/books"}}

	if rootsChanged(base, []LibraryRoot{{ID: "b", Path: "/srv/books/"}, {ID: "a", Path: "/srv/music"}}) {
		t.Fatal("reordering and trailing slashes should not count as a change")
	}
	if !rootsChanged(base, append(append([]LibraryRoot{}, base...), LibraryRoot{ID: "c", Path: "/srv/pods"})) {
		t.Fatal("an added library should count as a change")
	}
	if !rootsChanged(base, []LibraryRoot{{ID: "a", Path: "/srv/music2"}, {ID: "b", Path: "/srv/books"}}) {
		t.Fatal("a repointed library should count as a change")
	}
	if !rootsChanged(base, base[:1]) {
		t.Fatal("a removed library should count as a change")
	}
}

func TestDirSetForgetDropsChildren(t *testing.T) {
	watched := dirSet{
		"/srv/music/Artist":           {},
		"/srv/music/Artist/Album":     {},
		"/srv/music/Artist/Album/CD1": {},
		"/srv/music/Other":            {},
		"/srv/music/ArtistOther":      {},
	}
	if !watched.forget("/srv/music/Artist") {
		t.Fatal("forgetting a watched directory should report it was a directory")
	}
	for _, gone := range []string{"/srv/music/Artist", "/srv/music/Artist/Album", "/srv/music/Artist/Album/CD1"} {
		if _, ok := watched[gone]; ok {
			t.Fatalf("%q should have been forgotten", gone)
		}
	}
	// A sibling whose name merely shares a prefix must survive.
	if _, ok := watched["/srv/music/ArtistOther"]; !ok {
		t.Fatal("a prefix-sharing sibling should not be forgotten")
	}
	if watched.forget("/srv/music/never-watched.flac") {
		t.Fatal("a path that was never watched is not a directory")
	}
}

func TestPendingChangesReconcileSupersedesSubpaths(t *testing.T) {
	pending := newPendingChanges()
	pending.add("lib1", "/srv/music/New Album")
	pending.reconcile("lib1")
	pending.add("lib2", "/srv/books/Book")

	scans := pending.drain()
	if len(scans) != 2 {
		t.Fatalf("got %d scans, want 2", len(scans))
	}
	if !scans[0].full || len(scans[0].subpaths) != 0 {
		t.Fatalf("lib1 should reconcile the whole library, got %+v", scans[0])
	}
	if scans[1].full || len(scans[1].subpaths) != 1 {
		t.Fatalf("lib2 should stay incremental, got %+v", scans[1])
	}
	if len(pending.drain()) != 0 {
		t.Fatal("drain should leave the pending set empty")
	}
}
