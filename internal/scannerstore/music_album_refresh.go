package scannerstore

import (
	"context"
	"fmt"
)

// AlbumRefreshTrack is the per-track material an album refresh consolidates:
// the album title and artist each track claims, and whatever cover art it
// carries.
type AlbumRefreshTrack struct {
	Title         string
	AlbumTitle    string
	DisplayArtist string
	ImagesJSON    string
}

// AlbumIDsWithTracksInLibrary lists every album that has an indexed track in
// the library — the set a full scan re-derives.
func (s *Store) AlbumIDsWithTracksInLibrary(ctx context.Context, libraryID string) ([]string, error) {
	return s.stringColumn(ctx, "list albums for library refresh", `
		SELECT DISTINCT t.album_id
		FROM music_tracks t
		JOIN media_files mf ON mf.track_id = t.id
		WHERE mf.library_id = ? AND t.album_id IS NOT NULL AND TRIM(t.album_id) != ''`,
		libraryID)
}

// AlbumRefreshTracks returns an album's tracks in disc/track order.
func (s *Store) AlbumRefreshTracks(ctx context.Context, albumID string) ([]AlbumRefreshTrack, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT title, album_title, display_artist, images_json
		FROM music_tracks
		WHERE album_id = ?
		ORDER BY disc_number, track_number, title`, albumID)
	if err != nil {
		return nil, fmt.Errorf("load tracks for album refresh %q: %w", albumID, err)
	}
	defer rows.Close()

	var tracks []AlbumRefreshTrack
	for rows.Next() {
		var row AlbumRefreshTrack
		if err := rows.Scan(&row.Title, &row.AlbumTitle, &row.DisplayArtist, &row.ImagesJSON); err != nil {
			return nil, fmt.Errorf("scan track for album refresh: %w", err)
		}
		tracks = append(tracks, row)
	}
	return tracks, rows.Err()
}

// RefreshMusicAlbum writes back the consolidated title, artist and cover.
//
// Each column keeps its existing value when the incoming one is empty, so a
// refresh that could not determine a title does not erase the one already
// there. That is why every value is bound twice — once to test, once to use.
func (s *Store) RefreshMusicAlbum(ctx context.Context, albumID, title, displayArtist, imagesJSON string) error {
	_, err := s.exec(ctx, `
		UPDATE music_albums
		SET title = CASE WHEN ? != '' THEN ? ELSE title END,
		    display_artist = CASE WHEN ? != '' THEN ? ELSE display_artist END,
		    images_json = CASE
		      WHEN ? NOT IN ('[]', 'null', '')
		      THEN ?
		      ELSE images_json
		    END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		title, title,
		displayArtist, displayArtist,
		imagesJSON, imagesJSON,
		albumID)
	if err != nil {
		return fmt.Errorf("refresh music album %q: %w", albumID, err)
	}
	return nil
}
