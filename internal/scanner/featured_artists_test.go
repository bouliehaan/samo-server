package scanner

import "testing"

func TestStripFeaturedArtist(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Featured credits collapse to the primary artist (the reported pain).
		{"Mac Miller feat. Action Bronson", "Mac Miller"},
		{"Mac Miller feat. Big Weezy Bo Beezy", "Mac Miller"},
		{"Mac Miller ft. Action Bronson", "Mac Miller"},
		{"Mac Miller ft Action Bronson", "Mac Miller"},
		{"Mac Miller featuring Action Bronson", "Mac Miller"},
		{"Mac Miller (feat. Action Bronson)", "Mac Miller"},
		{"Mac Miller [feat. Action Bronson]", "Mac Miller"},
		{"Drake FEAT. Future", "Drake"},
		{"Calvin Harris feat. Rihanna & Some One", "Calvin Harris"},
		{"  Kanye West   feat.   Jay-Z  ", "Kanye West"},
		// Real names that merely CONTAIN the letters must be untouched.
		{"Daft Punk", "Daft Punk"},
		{"Soft Cell", "Soft Cell"},
		{"Feature", "Feature"},
		{"DJ Feat", "DJ Feat"},
		{"Earth, Wind & Fire", "Earth, Wind & Fire"},
		{"Tyler, The Creator", "Tyler, The Creator"},
		{"Crosby, Stills & Nash", "Crosby, Stills & Nash"},
		{"will.i.am", "will.i.am"},
		{"", ""},
		// A value that is ONLY a credit keeps the original (never empty).
		{"feat. Someone", "feat. Someone"},
	}
	for _, tc := range cases {
		if got := stripFeaturedArtist(tc.in); got != tc.want {
			t.Errorf("stripFeaturedArtist(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeFeaturedArtistNamesPreservesLength(t *testing.T) {
	in := []string{"A feat. B", "C", "D ft. E"}
	got := normalizeFeaturedArtistNames(in)
	if len(got) != len(in) {
		t.Fatalf("length changed: got %d want %d", len(got), len(in))
	}
	want := []string{"A", "C", "D"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("normalizeFeaturedArtistNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
