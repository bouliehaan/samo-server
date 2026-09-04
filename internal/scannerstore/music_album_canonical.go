package scannerstore

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
)

// MusicAlbumExists reports whether an album row is present.
func (s *Store) MusicAlbumExists(ctx context.Context, id string) bool {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM music_albums WHERE id = ? LIMIT 1`, id).Scan(&exists); err != nil {
		return false
	}
	return exists == 1
}

// CanonicalMusicAlbumID finds the album row already representing this record —
// same album artist, title and version once case and punctuation are folded
// away — and returns its id, or "" when the library has no such row.
//
// It exists because album identity is derived from tags, and tags disagree
// about the same record constantly: one track carries a 1987 date and another
// 2022, one carries the MusicBrainz release id of the original pressing and
// another of a reissue. Each difference minted a separate album, so a record
// arrived in the library as two, three, five rows holding a track or two each.
// Keying on what a person actually calls the record — artist, title, edition —
// collapses those back together.
//
// Ties break toward the row with the most tracks: it is the one already
// carrying the record, so the strays join it rather than the other way round.
func (s *Store) CanonicalMusicAlbumID(ctx context.Context, displayArtist, title, version string) (string, error) {
	artistKey, titleKey := albumFoldKey(displayArtist), albumFoldKey(title)
	if artistKey == "" || titleKey == "" {
		return "", nil
	}
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT a.id FROM music_albums a
		WHERE lower(regexp_replace(a.display_artist, '[^[:alnum:]]', '', 'g')) = ?
		  AND lower(regexp_replace(a.title,          '[^[:alnum:]]', '', 'g')) = ?
		  AND lower(regexp_replace(a.version,        '[^[:alnum:]]', '', 'g')) = ?
		ORDER BY (SELECT count(*) FROM music_tracks t WHERE t.album_id = a.id) DESC, a.id
		LIMIT 1`,
		artistKey, titleKey, albumFoldKey(version)).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(id), nil
}

// albumFoldKey mirrors the SQL fold above: letters and digits only, lowercased.
// It is what makes "Outlandos d’Amour" and "Outlandos D'Amour", or
// "To the 5 Boroughs" and "To The 5 Boroughs", the same record.
//
// Unicode-aware, matching Postgres's [[:alnum:]]: an ASCII-only fold reduces
// "25時のクレセント" to "25", and a key that thin would match any other album
// by that artist whose title happens to contain 25.
func albumFoldKey(value string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}
