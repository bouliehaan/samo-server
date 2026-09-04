package scanner

import (
	"context"
	"strings"
	"unicode"
)

// canonicalAlbumID resolves a freshly computed album id to the row the library
// already keeps for that record.
//
// Album ids are derived from tags, and tags disagree about the same record all
// the time — a differing date, a reissue's MusicBrainz release id — so the
// derivation alone split one album into several rows holding a track or two
// each. When the computed id names a row that does not exist, this asks whether
// the record does, under a name a person would recognize, and joins it instead
// of minting another row.
//
// An id that already exists is returned untouched: that is the overwhelmingly
// common path, it costs one indexed lookup, and it keeps every album already in
// the library exactly where it is.
func (s *Scanner) canonicalAlbumID(ctx context.Context, computed, displayArtist, title, version string) string {
	if s.store == nil || computed == "" {
		return computed
	}
	if s.store.MusicAlbumExists(ctx, computed) {
		return computed
	}
	if !albumCanonicalKeyUsable(displayArtist, title) {
		return computed
	}
	// Keyed on the RAW values, not the folded ones: the fold that decides a
	// match lives in SQL, and caching under a fold computed here would answer
	// for a key the query never agreed to if the two ever drifted apart.
	key := displayArtist + "\x1f" + title + "\x1f" + version
	if s.albumCanonical == nil {
		s.albumCanonical = map[string]string{}
	}
	// Cached per scan: a folder of strays all resolve to the same record, and
	// without this each track re-runs the same lookup.
	if cached, ok := s.albumCanonical[key]; ok {
		return cached
	}
	found, err := s.store.CanonicalMusicAlbumID(ctx, displayArtist, title, version)
	if err != nil || found == "" {
		// Remember the miss too — the first track of a genuinely new album
		// should not make every sibling re-ask.
		s.albumCanonical[key] = computed
		return computed
	}
	s.albumCanonical[key] = found
	return found
}

// albumCanonicalKeyUsable reports whether both halves of the record's identity
// survive folding. Neither may fold away: matching on an empty artist or title
// would join every album that also folds to nothing.
func albumCanonicalKeyUsable(displayArtist, title string) bool {
	return foldAlbumKey(displayArtist) != "" && foldAlbumKey(title) != ""
}

// foldAlbumKey matches scannerstore.CanonicalMusicAlbumID's SQL fold: letters
// and digits only, lowercased, Unicode-aware like Postgres's [[:alnum:]].
func foldAlbumKey(value string) string {
	out := make([]rune, 0, len(value))
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
		}
	}
	return string(out)
}
