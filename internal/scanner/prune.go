package scanner

import (
	"context"
	"fmt"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type ScanStats struct {
	FilesSeen   int
	FilesPruned int
	ItemsPruned int
}

type scanAccumulator struct {
	filePaths    map[string]struct{}
	audiobookIDs map[string]struct{}
	podcastIDs   map[string]struct{}
	episodeIDs   map[string]struct{}
	onFile       func(total int)
}

func newScanAccumulator() *scanAccumulator {
	return &scanAccumulator{
		filePaths:    map[string]struct{}{},
		audiobookIDs: map[string]struct{}{},
		podcastIDs:   map[string]struct{}{},
		episodeIDs:   map[string]struct{}{},
	}
}

func (a *scanAccumulator) seeFile(path string) {
	a.filePaths[strings.TrimSpace(path)] = struct{}{}
	if a.onFile != nil {
		a.onFile(len(a.filePaths))
	}
}

func (a *scanAccumulator) seeAudiobook(id string) {
	id = strings.TrimSpace(id)
	if id != "" {
		a.audiobookIDs[id] = struct{}{}
	}
}

func (a *scanAccumulator) seePodcast(id string) {
	id = strings.TrimSpace(id)
	if id != "" {
		a.podcastIDs[id] = struct{}{}
	}
}

func (a *scanAccumulator) seeEpisode(id string) {
	id = strings.TrimSpace(id)
	if id != "" {
		a.episodeIDs[id] = struct{}{}
	}
}

// ScanOptions threads per-scan behavior through ScanWithProgress without
// breaking the existing ScanWithStats signature used in tests.
type ScanOptions struct {
	// OnFileSeen fires after every file is recorded. `total` is the
	// running file count across all libraries scanned so far in this
	// invocation. Callers typically throttle the callback before doing
	// anything expensive (DB writes, log lines).
	OnFileSeen func(total int)
}

func (s *Scanner) ScanWithStats(ctx context.Context, libraries []Library) (ScanStats, error) {
	return s.ScanWithProgress(ctx, libraries, ScanOptions{})
}

// ScanWithProgress is the progress-aware sibling of ScanWithStats. The
// libraries package wires its OnFileSeen callback to update the
// scan_jobs.files_seen column so the dashboard can poll live progress.
func (s *Scanner) ScanWithProgress(ctx context.Context, libraries []Library, opts ScanOptions) (ScanStats, error) {
	idx, err := catalog.LoadOverrideIndex(ctx, s.db)
	if err != nil {
		return ScanStats{}, err
	}
	if !idx.IsEmpty() {
		s.overrideIndex = idx
	}
	defer func() { s.overrideIndex = nil }()

	stats := ScanStats{}
	// A scan failure used to skip refreshStats entirely, leaving every
	// library at item_count=0 forever — even libraries that scanned
	// fine before the failing one threw. The dashboard would then claim
	// "0 items" across the board regardless of how many tracks landed
	// in the catalog. Track the first error but always run post-scan
	// bookkeeping so partial progress is reflected.
	var firstErr error
	for _, library := range libraries {
		// The callback's `total` should be cumulative across libraries
		// so the dashboard's counter only ever climbs.
		baseline := stats.FilesSeen
		var cb func(int)
		if opts.OnFileSeen != nil {
			cb = func(perLibrary int) { opts.OnFileSeen(baseline + perLibrary) }
		}
		libraryStats, err := s.scanLibraryWithStats(ctx, library, cb)
		stats.FilesSeen += libraryStats.FilesSeen
		stats.FilesPruned += libraryStats.FilesPruned
		stats.ItemsPruned += libraryStats.ItemsPruned
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := s.refreshStats(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.pruneOrphanMusic(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := catalog.PruneStaleMetadataOverrides(ctx, s.db); err != nil && firstErr == nil {
		firstErr = err
	}
	return stats, firstErr
}

func (s *Scanner) scanLibraryWithStats(ctx context.Context, library Library, onFileSeen func(int)) (ScanStats, error) {
	accumulator := newScanAccumulator()
	accumulator.onFile = onFileSeen
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

	switch library.Kind {
	case "audiobook":
		pruned, err := s.pruneAudiobooks(ctx, library.ID, accumulator.audiobookIDs)
		if err != nil {
			return stats, err
		}
		stats.ItemsPruned += pruned
	case "podcast":
		pruned, err := s.prunePodcasts(ctx, library.ID, accumulator.podcastIDs)
		if err != nil {
			return stats, err
		}
		stats.ItemsPruned += pruned
		episodePruned, err := s.prunePodcastEpisodes(ctx, library.ID, accumulator.episodeIDs)
		if err != nil {
			return stats, err
		}
		stats.ItemsPruned += episodePruned
	case "mixed":
		pruned, err := s.pruneAudiobooks(ctx, library.ID, accumulator.audiobookIDs)
		if err != nil {
			return stats, err
		}
		stats.ItemsPruned += pruned
		podPruned, err := s.prunePodcasts(ctx, library.ID, accumulator.podcastIDs)
		if err != nil {
			return stats, err
		}
		stats.ItemsPruned += podPruned
		episodePruned, err := s.prunePodcastEpisodes(ctx, library.ID, accumulator.episodeIDs)
		if err != nil {
			return stats, err
		}
		stats.ItemsPruned += episodePruned
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

func (s *Scanner) pruneAudiobooks(ctx context.Context, libraryID string, seen map[string]struct{}) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM audiobooks WHERE library_id = ?`, libraryID)
	if err != nil {
		return 0, fmt.Errorf("list audiobooks for prune: %w", err)
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan audiobook id: %w", err)
		}
		if _, ok := seen[id]; !ok {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, id := range stale {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM audiobooks WHERE id = ?`, id); err != nil {
			return 0, fmt.Errorf("delete stale audiobook %q: %w", id, err)
		}
	}
	return len(stale), nil
}

func (s *Scanner) prunePodcasts(ctx context.Context, libraryID string, seen map[string]struct{}) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM podcasts WHERE library_id = ?`, libraryID)
	if err != nil {
		return 0, fmt.Errorf("list podcasts for prune: %w", err)
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan podcast id: %w", err)
		}
		if _, ok := seen[id]; !ok {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, id := range stale {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM podcasts WHERE id = ?`, id); err != nil {
			return 0, fmt.Errorf("delete stale podcast %q: %w", id, err)
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
