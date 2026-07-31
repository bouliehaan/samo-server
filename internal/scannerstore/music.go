package scannerstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// UpsertMusicArtist writes the artist row and reports whether it was new.
//
// "New" is the fact the scanner needs for its accumulator (a scan reports how
// many artists it discovered), so it is answered here — from a probe taken
// before the write — rather than inferred afterwards from a row count that an
// idempotent ON CONFLICT update makes meaningless.
//
// images_json is preserved when the incoming value is empty: artist images are
// fetched asynchronously by the artistimages backfill, and a scan that merely
// re-read the tags must not blank out artwork it never had.
func (s *Store) UpsertMusicArtist(ctx context.Context, artist catalog.MusicArtist) (created bool, err error) {
	existed := false
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM music_artists WHERE id = ? LIMIT 1`, artist.ID).Scan(new(int)); err == nil {
		existed = true
	}
	if _, err := s.exec(ctx, `
		INSERT INTO music_artists (id, name, sort_name, genres_json, images_json, external_ids_json, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  sort_name = excluded.sort_name,
		  genres_json = excluded.genres_json,
		  images_json = CASE
		    WHEN excluded.images_json IS NULL OR excluded.images_json IN ('[]', 'null', '')
		    THEN music_artists.images_json
		    ELSE excluded.images_json
		  END,
		  external_ids_json = excluded.external_ids_json,
		  updated_at = CURRENT_TIMESTAMP`,
		artist.ID, artist.Name, artist.SortName, jsonText(artist.Genres), jsonText(artist.Images), jsonText(artist.ExternalIDs)); err != nil {
		return false, fmt.Errorf("upsert music artist %q: %w", artist.Name, err)
	}
	return !existed, nil
}

// UpsertMusicAlbum writes the album row.
//
// display_artist and images_json are both preserved when the incoming value is
// empty. A track whose tags name no album artist would otherwise blank a name
// resolved from a better-tagged sibling on the previous file.
func (s *Store) UpsertMusicAlbum(ctx context.Context, album catalog.MusicAlbum) error {
	_, err := s.exec(ctx, `
		INSERT INTO music_albums (
		  id, title, sort_title, version, display_artist, release_date, original_release_date, release_year, release_type,
		  release_status, compilation, record_label, catalog_number, barcode, genres_json, styles_json, moods_json,
		  tags_json, images_json, external_ids_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  title = excluded.title,
		  sort_title = excluded.sort_title,
		  version = excluded.version,
		  display_artist = CASE
		    WHEN excluded.display_artist IS NULL OR TRIM(excluded.display_artist) = ''
		    THEN music_albums.display_artist
		    ELSE excluded.display_artist
		  END,
		  release_date = excluded.release_date,
		  original_release_date = excluded.original_release_date,
		  release_year = excluded.release_year,
		  release_type = excluded.release_type,
		  release_status = excluded.release_status,
		  compilation = excluded.compilation,
		  record_label = excluded.record_label,
		  catalog_number = excluded.catalog_number,
		  barcode = excluded.barcode,
		  genres_json = excluded.genres_json,
		  styles_json = excluded.styles_json,
		  moods_json = excluded.moods_json,
		  tags_json = excluded.tags_json,
		  images_json = CASE
		    WHEN excluded.images_json IS NULL OR excluded.images_json IN ('[]', 'null', '')
		    THEN music_albums.images_json
		    ELSE excluded.images_json
		  END,
		  external_ids_json = excluded.external_ids_json,
		  updated_at = CURRENT_TIMESTAMP`,
		album.ID, album.Title, album.SortTitle, album.Version, album.DisplayArtist, album.ReleaseDate, album.OriginalReleaseDate, album.ReleaseYear,
		album.ReleaseType, album.ReleaseStatus, boolInt(album.Compilation), album.RecordLabel, album.CatalogNumber, album.Barcode,
		jsonText(album.Genres), jsonText(album.Styles), jsonText(album.Moods), jsonText(album.Tags),
		jsonText(album.Images), jsonText(album.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert music album %q: %w", album.Title, err)
	}
	return nil
}

// AlbumArtistNames returns the album's credited artist names in credit order.
func (s *Store) AlbumArtistNames(ctx context.Context, albumID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.name
		FROM music_album_artists aa
		JOIN music_artists a ON a.id = aa.artist_id
		WHERE aa.album_id = ?
		ORDER BY aa.position`, albumID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := make([]string, 0, 2)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return names, err
		}
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names, rows.Err()
}

// CountAlbumArtists reports how many artists are already credited on the album.
// The scanner uses it to leave a well-credited album alone when the track it is
// currently reading carries only weak, inferred artist tags.
func (s *Store) CountAlbumArtists(ctx context.Context, albumID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_album_artists WHERE album_id = ?`, albumID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count album artists: %w", err)
	}
	return count, nil
}

// ReplaceAlbumArtists rewrites the album's artist credits, in the given order.
func (s *Store) ReplaceAlbumArtists(ctx context.Context, albumID string, artistIDs []string) error {
	if _, err := s.exec(ctx, `DELETE FROM music_album_artists WHERE album_id = ?`, albumID); err != nil {
		return fmt.Errorf("clear album artists: %w", err)
	}
	for index, artistID := range artistIDs {
		if _, err := s.exec(ctx, `
			INSERT INTO music_album_artists (album_id, artist_id, position)
			VALUES (?, ?, ?)`,
			albumID, artistID, index); err != nil {
			return fmt.Errorf("insert album artist: %w", err)
		}
	}
	return nil
}

// UpsertMusicTrack writes the track row.
func (s *Store) UpsertMusicTrack(ctx context.Context, track catalog.MusicTrack) error {
	_, err := s.exec(ctx, `
		INSERT INTO music_tracks (
		  id, title, sort_title, subtitle, display_artist, album_id, album_title, disc_number, track_number, total_discs,
		  total_tracks, release_date, release_year, genres_json, moods_json, tags_json, duration_seconds,
		  explicit, bpm, musical_key, comment, lyrics_json, images_json, external_ids_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  title = excluded.title,
		  sort_title = excluded.sort_title,
		  subtitle = excluded.subtitle,
		  display_artist = excluded.display_artist,
		  album_id = excluded.album_id,
		  album_title = excluded.album_title,
		  disc_number = excluded.disc_number,
		  track_number = excluded.track_number,
		  total_discs = excluded.total_discs,
		  total_tracks = excluded.total_tracks,
		  release_date = excluded.release_date,
		  release_year = excluded.release_year,
		  genres_json = excluded.genres_json,
		  moods_json = excluded.moods_json,
		  tags_json = excluded.tags_json,
		  duration_seconds = excluded.duration_seconds,
		  explicit = excluded.explicit,
		  bpm = excluded.bpm,
		  musical_key = excluded.musical_key,
		  comment = excluded.comment,
		  lyrics_json = excluded.lyrics_json,
		  images_json = CASE
		    WHEN excluded.images_json IS NULL OR excluded.images_json IN ('[]', 'null', '')
		    THEN music_tracks.images_json
		    ELSE excluded.images_json
		  END,
		  external_ids_json = excluded.external_ids_json,
		  updated_at = CURRENT_TIMESTAMP`,
		track.ID, track.Title, track.SortTitle, track.Subtitle, track.DisplayArtist, nullableString(track.AlbumID), track.AlbumTitle,
		track.DiscNumber, track.TrackNumber, track.TotalDiscs, track.TotalTracks, track.ReleaseDate, track.ReleaseYear,
		jsonText(track.Genres), jsonText(track.Moods), jsonText(track.Tags), track.DurationSeconds, boolInt(track.Explicit),
		track.BPM, track.Key, track.Comment, jsonText(track.Lyrics), jsonText(track.Images), jsonText(track.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert music track %q: %w", track.Title, err)
	}
	return nil
}

// ReplaceTrackArtists rewrites the track's performing credits, in order.
func (s *Store) ReplaceTrackArtists(ctx context.Context, trackID string, artistIDs []string) error {
	if _, err := s.exec(ctx, `DELETE FROM music_track_artists WHERE track_id = ?`, trackID); err != nil {
		return fmt.Errorf("clear track artists: %w", err)
	}
	for index, artistID := range artistIDs {
		if _, err := s.exec(ctx, `
			INSERT INTO music_track_artists (track_id, artist_id, role, position)
			VALUES (?, ?, 'artist', ?)`,
			trackID, artistID, index); err != nil {
			return fmt.Errorf("insert track artist: %w", err)
		}
	}
	return nil
}

// UpsertGenre records a genre name for a media kind. Idempotent.
func (s *Store) UpsertGenre(ctx context.Context, kind, name string) error {
	_, err := s.exec(ctx, `
		INSERT INTO genres (name, kind)
		VALUES (?, ?)
		ON CONFLICT(name, kind) DO NOTHING`,
		name, kind)
	return err
}
