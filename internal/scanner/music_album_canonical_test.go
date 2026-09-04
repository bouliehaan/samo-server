package scanner

import "testing"

// The fold has to erase exactly what tag editors disagree about and nothing
// else. These are the real pairs that split records in the library.
func TestFoldAlbumKeyCollapsesTagPunctuation(t *testing.T) {
	same := [][2]string{
		{"Outlandos d’Amour", "Outlandos D'Amour"}, // typographic vs ASCII apostrophe
		{"To the 5 Boroughs", "To The 5 Boroughs"}, // capitalisation
		{"Elvis`Christmas Album", "Elvis' Christmas Album"},
		{"(What's The Story) Morning Glory?", "Whats the Story Morning Glory"},
	}
	for _, pair := range same {
		if foldAlbumKey(pair[0]) != foldAlbumKey(pair[1]) {
			t.Fatalf("%q and %q should fold alike, got %q vs %q",
				pair[0], pair[1], foldAlbumKey(pair[0]), foldAlbumKey(pair[1]))
		}
	}
	// Different records must stay different — folding is not licence to merge.
	if foldAlbumKey("In Utero") == foldAlbumKey("In Utero Demos") {
		t.Fatal("a different record must not fold onto In Utero")
	}
}

// A record is only matchable when both halves of its identity survive the
// fold: matching on an empty artist or title would join every album that also
// folds to nothing.
func TestAlbumCanonicalKeyRequiresBothHalves(t *testing.T) {
	if !albumCanonicalKeyUsable("Nirvana", "In Utero") {
		t.Fatal("a normal album must be matchable")
	}
	for _, bad := range [][2]string{{"", "In Utero"}, {"Nirvana", ""}, {"Nirvana", "…"}} {
		if albumCanonicalKeyUsable(bad[0], bad[1]) {
			t.Fatalf("albumCanonicalKeyUsable(%q,%q) = true, want false", bad[0], bad[1])
		}
	}
}

// The fold is Unicode-aware, matching Postgres's [[:alnum:]]. An ASCII-only
// fold reduced "25時のクレセント" to "25" — a key thin enough to match any other
// album by that artist with a 25 in the title.
func TestFoldAlbumKeyKeepsNonLatinTitles(t *testing.T) {
	if got := foldAlbumKey("25時のクレセント"); got != "25時のクレセント" {
		t.Fatalf("foldAlbumKey = %q, want the title intact", got)
	}
	if foldAlbumKey("色彩感覚") == "" {
		t.Fatal("a wholly non-Latin title must survive folding")
	}
	if foldAlbumKey("25時のクレセント") == foldAlbumKey("25 Hours") {
		t.Fatal("distinct titles must not fold together on their digits")
	}
}

// Without a store there is nothing to canonicalise against, and the computed
// id has to come back untouched rather than empty.
func TestCanonicalAlbumIDWithoutStoreIsAPassthrough(t *testing.T) {
	s := &Scanner{}
	if got := s.canonicalAlbumID(t.Context(), "album_abc", "Nirvana", "In Utero", ""); got != "album_abc" {
		t.Fatalf("got %q, want album_abc", got)
	}
}
