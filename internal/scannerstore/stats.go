package scannerstore

import (
	"context"
	"fmt"
)

// refreshStatements recompute the denormalized count and duration columns that
// catalog reads project to clients. Each is a full-table recompute from the
// authoritative rows rather than an incremental adjustment, so a drifted count
// — from a partial scan, an interrupted delete, a migration — is corrected
// rather than compounded.
var refreshStatements = []string{
	`UPDATE music_albums
	 SET track_count = (SELECT COUNT(*) FROM music_tracks WHERE album_id = music_albums.id),
	     duration_seconds = COALESCE((SELECT SUM(duration_seconds) FROM music_tracks WHERE album_id = music_albums.id), 0),
	     disc_count = COALESCE((SELECT MAX(disc_number) FROM music_tracks WHERE album_id = music_albums.id), 0)`,
	`UPDATE music_artists
	 SET track_count = COALESCE((SELECT COUNT(DISTINCT track_id) FROM music_track_artists WHERE artist_id = music_artists.id), 0),
	     album_count = COALESCE((SELECT COUNT(DISTINCT album_id) FROM music_album_artists WHERE artist_id = music_artists.id), 0),
	     duration_seconds = COALESCE((
	       SELECT SUM(t.duration_seconds)
	       FROM music_tracks t
	       JOIN music_track_artists ta ON ta.track_id = t.id
	       WHERE ta.artist_id = music_artists.id
	     ), 0)`,
	// Audiobook and podcast durations fall back to the existing value rather
	// than 0: a show whose files are temporarily missing keeps its runtime
	// instead of collapsing to zero in every list that sorts by it.
	`UPDATE audiobooks
	 SET duration_seconds = COALESCE((SELECT SUM(duration_seconds) FROM media_files WHERE audiobook_id = audiobooks.id), duration_seconds)`,
	`UPDATE podcasts
	 SET duration_seconds = COALESCE((SELECT SUM(duration_seconds) FROM media_files WHERE podcast_id = podcasts.id), duration_seconds)`,
	`UPDATE contributors
	 SET item_count = COALESCE((SELECT COUNT(DISTINCT audiobook_id) FROM audiobook_contributors WHERE contributor_id = contributors.id), 0),
	     duration_seconds = COALESCE((
	       SELECT SUM(a.duration_seconds)
	       FROM audiobooks a
	       JOIN audiobook_contributors ac ON ac.audiobook_id = a.id
	       WHERE ac.contributor_id = contributors.id
	     ), 0)`,
	`UPDATE series
	 SET item_count = COALESCE((SELECT COUNT(DISTINCT audiobook_id) FROM audiobook_series WHERE series_id = series.id), 0),
	     duration_seconds = COALESCE((
	       SELECT SUM(a.duration_seconds)
	       FROM audiobooks a
	       JOIN audiobook_series aas ON aas.audiobook_id = a.id
	       WHERE aas.series_id = series.id
	     ), 0)`,
	// libraries.item_count surfaces on the home dashboard and the settings
	// "attached libraries" panel. Count what a human would count for that kind:
	//   - music:     distinct music_tracks
	//   - audiobook: rows in audiobooks
	//   - podcast:   rows in podcasts (the show, not episodes)
	//   - mixed:     all three summed (any combination the scanner
	//                discovered in this root)
	// This statement runs every Scan, including partial-failure scans, so
	// counts stay current even when one library throws.
	`UPDATE libraries
	 SET item_count = CASE
	   WHEN kind = 'music' THEN COALESCE((
	     SELECT COUNT(DISTINCT t.id)
	     FROM music_tracks t
	     JOIN media_files mf ON mf.track_id = t.id
	     WHERE mf.library_id = libraries.id
	   ), 0)
	   WHEN kind = 'audiobook' THEN COALESCE((SELECT COUNT(*) FROM audiobooks WHERE library_id = libraries.id), 0)
	   WHEN kind = 'podcast' THEN COALESCE((SELECT COUNT(*) FROM podcasts WHERE library_id = libraries.id), 0)
	   WHEN kind = 'mixed' THEN COALESCE((
	     SELECT COUNT(DISTINCT t.id)
	     FROM music_tracks t
	     JOIN media_files mf ON mf.track_id = t.id
	     WHERE mf.library_id = libraries.id
	   ), 0)
	     + COALESCE((SELECT COUNT(*) FROM audiobooks WHERE library_id = libraries.id), 0)
	     + COALESCE((SELECT COUNT(*) FROM podcasts WHERE library_id = libraries.id), 0)
	   ELSE item_count
	 END`,
}

// RefreshAggregateStats recomputes every denormalized count and duration column.
func (s *Store) RefreshAggregateStats(ctx context.Context) error {
	for _, statement := range refreshStatements {
		if _, err := s.exec(ctx, statement); err != nil {
			return fmt.Errorf("refresh scanner stats: %w", err)
		}
	}
	return nil
}
