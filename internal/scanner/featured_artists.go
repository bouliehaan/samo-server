package scanner

import (
	"regexp"
	"strings"
)

// featuredArtistPattern matches a "featuring" credit appended to an artist-tag
// value — " feat. X", " ft X", " featuring X", "(feat. X)", "[feat. X]" — so the
// PRIMARY artist can be used as the catalog artist entity. Without this, every
// distinct "Primary feat. Guest" string mints its OWN artist in the browse list
// (the reported "Mac Miller feat. Action Bronson" / "Mac Miller feat. Big Weezy"
// explosion), because artist ids are derived from the name.
//
// Word boundaries are deliberate: they keep this from matching real names that
// merely CONTAIN the letters — "Daft Punk", "Soft Cell", "Feature" — and the
// required trailing "\s+.*" keeps a name that simply ENDS in the word (e.g. an
// artist literally called "DJ Feat") from being truncated.
var featuredArtistPattern = regexp.MustCompile(`(?i)\s*[\(\[]?\s*\b(?:feat|ft|featuring)\b\.?\s+.*$`)

// stripFeaturedArtist returns the primary artist from a raw artist-tag value,
// dropping any trailing "feat./ft./featuring …" credit. Returns the value
// unchanged when there is no such credit, and never collapses to empty.
func stripFeaturedArtist(name string) string {
	trimmed := strings.TrimSpace(name)
	stripped := strings.TrimSpace(featuredArtistPattern.ReplaceAllString(trimmed, ""))
	if stripped == "" {
		// The value was only a credit ("feat. X") — keep the original rather
		// than mint an empty artist.
		return trimmed
	}
	return stripped
}

// normalizeFeaturedArtistNames strips featured credits from every name while
// preserving slice length, so the positional zip with sort names / MusicBrainz
// ids in musicArtistsFromNames stays aligned.
func normalizeFeaturedArtistNames(names []string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = stripFeaturedArtist(name)
	}
	return out
}
