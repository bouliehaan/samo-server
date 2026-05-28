package scanner

import "testing"

func TestPathSeenNormalizesPaths(t *testing.T) {
	seen := buildSeenPathSet(map[string]struct{}{
		"/music/Artist/Album/track.flac": {},
	})
	if !pathSeen(seen, "/music/Artist/Album/./track.flac") {
		t.Fatal("expected clean path to match seen set")
	}
	if pathSeen(seen, "/music/Other/track.flac") {
		t.Fatal("unexpected match")
	}
}
