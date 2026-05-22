package scanner

import (
	"context"
	"fmt"
	"strings"
)

type ScanStats struct {
	FilesSeen   int
	FilesPruned int
	ItemsPruned int
}

type scanAccumulator struct {
	filePaths  map[string]struct{}
	itemIDs    map[string]struct{}
	episodeIDs map[string]struct{}
}

func newScanAccumulator() *scanAccumulator {
	return &scanAccumulator{
		filePaths:  map[string]struct{}{},
		itemIDs:    map[string]struct{}{},
		episodeIDs: map[string]struct{}{},
	}
}

func (a *scanAccumulator) seeFile(path string) {
	a.filePaths[strings.TrimSpace(path)] = struct{}{}
}

func (a *scanAccumulator) seeItem(id string) {
	id = strings.TrimSpace(id)
	if id != "" {
		a.itemIDs[id] = struct{}{}
	}
}

func (a *scanAccumulator) seeEpisode(id string) {
	id = strings.TrimSpace(id)
	if id != "" {
		a.episodeIDs[id] = struct{}{}
	}
}

func (s *Scanner) ScanWithStats(ctx context.Context, libraries []Library) (ScanStats, error) {
	stats := ScanStats{}
	for _, library := range libraries {
		libraryStats, err := s.scanLibraryWithStats(ctx, library)
		stats.FilesSeen += libraryStats.FilesSeen
		stats.FilesPruned += libraryStats.FilesPruned
		stats.ItemsPruned += libraryStats.ItemsPruned
		if err != nil {
			return stats, err
		}
	}
	if err := s.refreshStats(ctx); err != nil {
		return stats, err
	}
	if err := s.pruneOrphanMusic(ctx); err != nil {
		return stats, err
	}
	return stats, nil
}

func (s *Scanner) scanLibraryWithStats(ctx context.Context, library Library) (ScanStats, error) {
	accumulator := newScanAccumulator()
	s.activeScan = accumulator
	defer func() { s.activeScan = nil }()

	if err := s.scanLibrary(ctx, library); err != nil {
		return ScanStats{}, err
	}

	stats := ScanStats{FilesSeen: len(accumulator.filePaths)}
	pruneStats, err := s.pruneLibrary(ctx, library, accumulator)
	stats.FilesPruned = pruneStats.FilesPruned
	stats.ItemsPruned = pruneStats.ItemsPruned
	return stats, err
}

func (s *Scanner) pruneLibrary(ctx context.Context, library Library, accumulator *scanAccumulator) (ScanStats, error) {
	if strings.HasPrefix(library.Path, "samo://") {
		return ScanStats{}, nil
	}

	stats := ScanStats{}

	if library.Kind == "shelf" {
		itemPruned, err := s.pruneShelfItems(ctx, library.ID, accumulator.itemIDs)
		if err != nil {
			return stats, err
		}
		stats.ItemsPruned += itemPruned

		if library.MediaType == "podcast" {
			episodePruned, err := s.prunePodcastEpisodes(ctx, library.ID, accumulator.episodeIDs)
			if err != nil {
				return stats, err
			}
			stats.ItemsPruned += episodePruned
		}
	}

	filePruned, err := s.pruneMediaFiles(ctx, library.ID, accumulator.filePaths)
	if err != nil {
		return stats, err
	}
	stats.FilesPruned = filePruned
	return stats, nil
}

func (s *Scanner) pruneMediaFiles(ctx context.Context, libraryID string, seenPaths map[string]struct{}) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM media_files WHERE library_id = ?`, libraryID)
	if err != nil {
		return 0, fmt.Errorf("list media files for prune: %w", err)
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return 0, fmt.Errorf("scan media file path: %w", err)
		}
		if _, ok := seenPaths[path]; !ok {
			stale = append(stale, path)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, path := range stale {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM media_files WHERE library_id = ? AND path = ?`, libraryID, path); err != nil {
			return 0, fmt.Errorf("delete stale media file %q: %w", path, err)
		}
	}
	return len(stale), nil
}

func (s *Scanner) pruneShelfItems(ctx context.Context, libraryID string, seenItems map[string]struct{}) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM shelf_items WHERE library_id = ?`, libraryID)
	if err != nil {
		return 0, fmt.Errorf("list shelf items for prune: %w", err)
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan shelf item id: %w", err)
		}
		if _, ok := seenItems[id]; !ok {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, id := range stale {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM shelf_items WHERE id = ?`, id); err != nil {
			return 0, fmt.Errorf("delete stale shelf item %q: %w", id, err)
		}
	}
	return len(stale), nil
}

func (s *Scanner) prunePodcastEpisodes(ctx context.Context, libraryID string, seenEpisodes map[string]struct{}) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM podcast_episodes WHERE library_id = ?`, libraryID)
	if err != nil {
		return 0, fmt.Errorf("list podcast episodes for prune: %w", err)
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan podcast episode id: %w", err)
		}
		if _, ok := seenEpisodes[id]; !ok {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, id := range stale {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM podcast_episodes WHERE id = ?`, id); err != nil {
			return 0, fmt.Errorf("delete stale podcast episode %q: %w", id, err)
		}
	}
	return len(stale), nil
}

func (s *Scanner) pruneOrphanMusic(ctx context.Context) error {
	statements := []string{
		`DELETE FROM music_tracks
		 WHERE id NOT IN (SELECT track_id FROM media_files WHERE track_id IS NOT NULL)`,
		`DELETE FROM music_albums
		 WHERE id NOT IN (SELECT album_id FROM music_tracks WHERE album_id IS NOT NULL)`,
		`DELETE FROM music_artists
		 WHERE id NOT IN (SELECT artist_id FROM music_track_artists)
		   AND id NOT IN (SELECT artist_id FROM music_album_artists)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prune orphan music rows: %w", err)
		}
	}
	return nil
}
