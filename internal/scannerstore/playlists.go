package scannerstore

import (
	"context"
	"fmt"
)

// PlaylistTrackRefs is a playlist's stored track list, as raw JSON.
type PlaylistTrackRefs struct {
	ID           string
	TrackIDsJSON string
	TrackCount   int
}

// PlaylistTrackReferences lists every playlist's track ids, for the pass that
// rewrites them after tracks change identity during a scan.
func (s *Store) PlaylistTrackReferences(ctx context.Context) ([]PlaylistTrackRefs, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, track_ids_json, track_count
		FROM music_playlists`)
	if err != nil {
		return nil, fmt.Errorf("list playlists for track remap: %w", err)
	}
	defer rows.Close()

	var out []PlaylistTrackRefs
	for rows.Next() {
		var row PlaylistTrackRefs
		if err := rows.Scan(&row.ID, &row.TrackIDsJSON, &row.TrackCount); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// SetPlaylistTrackIDs rewrites a playlist's track list and count.
func (s *Store) SetPlaylistTrackIDs(ctx context.Context, playlistID string, trackIDs []string) error {
	if _, err := s.exec(ctx, `
		UPDATE music_playlists
		SET track_ids_json = ?,
		    track_count = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		jsonText(trackIDs), len(trackIDs), playlistID); err != nil {
		return fmt.Errorf("update playlist %q track ids: %w", playlistID, err)
	}
	return nil
}
