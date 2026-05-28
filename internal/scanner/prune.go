package scanner

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type ScanStats struct {
	FilesSeen    int
	FilesPruned  int
	ItemsPruned  int
	FilesMarked  int
	NewArtistIDs []string
}

type scanAccumulator struct {
	filePaths       map[string]struct{}
	audiobookIDs    map[string]struct{}
	podcastIDs      map[string]struct{}
	episodeIDs      map[string]struct{}
	touchedAlbumIDs map[string]struct{}
	newArtistIDs    map[string]struct{}
	onFile          func(total int)
}

func newScanAccumulator() *scanAccumulator {
	return &scanAccumulator{
		filePaths:       map[string]struct{}{},
		audiobookIDs:    map[string]struct{}{},
		podcastIDs:      map[string]struct{}{},
		episodeIDs:      map[string]struct{}{},
		touchedAlbumIDs: map[string]struct{}{},
		newArtistIDs:    map[string]struct{}{},
	}
}

func (a *scanAccumulator) seeAlbum(id string) {
	id = strings.TrimSpace(id)
	if id != "" {
		a.touchedAlbumIDs[id] = struct{}{}
	}
}

func (a *scanAccumulator) noteNewArtist(id string) {
	id = strings.TrimSpace(id)
	if id != "" {
		a.newArtistIDs[id] = struct{}{}
	}
}

func (a *scanAccumulator) seeFile(path string) {
	a.filePaths[normalizeScanPath(path)] = struct{}{}
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
	// OnWalkProgress fires while enumerating files before probing (phase 1 walk).
	OnWalkProgress func(seen int)
	// OnActivity reports the current high-level scan step for the dashboard.
	OnActivity func(message string)
	// JobID lets the external-scan subprocess persist progress to scan_jobs.
	JobID string
	// OnFileActive fires when a file scan begins (before ffprobe). Used to
	// surface the current path on scan_jobs for remote diagnostics.
	OnFileActive func(path string)
	// Mode selects full metadata probing or a quick checksum-only rescan.
	// Defaults to ScanModeFull when empty.
	Mode string
	// Subpaths limits scanning to files under these absolute directories.
	// Partial scans skip prune so untouched catalog entries stay intact.
	Subpaths []string
}

func (s *Scanner) ScanWithStats(ctx context.Context, libraries []Library) (ScanStats, error) {
	return s.ScanWithProgress(ctx, libraries, ScanOptions{})
}

// ScanWithProgress is the progress-aware sibling of ScanWithStats. The
// libraries package wires its OnFileSeen callback to update the
// scan_jobs.files_seen column so the dashboard can poll live progress.
func (s *Scanner) ScanWithProgress(ctx context.Context, libraries []Library, opts ScanOptions) (ScanStats, error) {
	if s.externalScanner && !IsScanSubprocess() {
		return s.ScanWithProgressExternal(ctx, libraries, opts)
	}
	s.trackIDMigrations = map[string]string{}
	if opts.OnActivity != nil {
		opts.OnActivity("loading metadata overrides")
	}
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
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		// The callback's `total` should be cumulative across libraries
		// so the dashboard's counter only ever climbs.
		baseline := stats.FilesSeen
		var cb func(int)
		if opts.OnFileSeen != nil {
			cb = func(perLibrary int) { opts.OnFileSeen(baseline + perLibrary) }
		}
		var walkCB func(int)
		if opts.OnWalkProgress != nil {
			walkCB = func(perLibrary int) { opts.OnWalkProgress(baseline + perLibrary) }
		}
		libraryStats, err := s.scanLibraryWithStats(ctx, library, cb, walkCB, opts.OnActivity, opts.OnFileActive, normalizeScanMode(opts.Mode), normalizeSubpaths(opts.Subpaths))
		stats.FilesSeen += libraryStats.FilesSeen
		stats.FilesPruned += libraryStats.FilesPruned
		stats.ItemsPruned += libraryStats.ItemsPruned
		stats.FilesMarked += libraryStats.FilesMarked
		stats.NewArtistIDs = appendUniqueStrings(stats.NewArtistIDs, libraryStats.NewArtistIDs)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil && len(libraries) > 1 {
		if err := s.runPhaseCrossLibraryMoves(ctx, libraries); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil && errors.Is(firstErr, context.Canceled) {
		return stats, firstErr
	}
	if err := s.RefreshStats(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if _, err := s.reconcilePlaylistTrackReferences(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if orphanPruned, err := s.pruneOrphanMusic(ctx); err != nil && firstErr == nil {
		firstErr = err
	} else {
		stats.ItemsPruned += orphanPruned
	}
	if err := catalog.PruneStaleMetadataOverrides(ctx, s.db); err != nil && firstErr == nil {
		firstErr = err
	}
	return stats, firstErr
}

func (s *Scanner) scanLibraryWithStats(ctx context.Context, library Library, onFileSeen, onWalkProgress func(int), onActivityMsg func(string), onFileActive func(string), mode string, subpaths []string) (ScanStats, error) {
	accumulator := newScanAccumulator()
	accumulator.onFile = onFileSeen
	s.activeScan = accumulator
	s.onWalkProgress = onWalkProgress
	s.onActivity = onActivityMsg
	s.onFileActive = onFileActive
	s.scanMode = mode
	s.scanSubpaths = subpaths
	if onActivityMsg != nil {
		onActivityMsg(fmt.Sprintf("library %q (%s)", library.Name, mode))
	}
	if mode == ScanModeQuick {
		if onActivityMsg != nil {
			onActivityMsg(fmt.Sprintf("loading index for %q", library.Name))
		}
		index, err := s.loadFileIndex(ctx, library.ID)
		if err != nil {
			s.activeScan = nil
			s.onWalkProgress = nil
			s.scanSubpaths = nil
			return ScanStats{}, err
		}
		s.fileIndex = index
	}
	defer func() {
		s.activeScan = nil
		s.onWalkProgress = nil
		s.onActivity = nil
		s.onFileActive = nil
		s.scanMode = ""
		s.fileIndex = nil
		s.scanSubpaths = nil
	}()

	state := newScanState(mode == ScanModeFull || mode == ScanModeRepair, mode, subpaths)
	stats, err := s.runLibraryPipeline(ctx, library, accumulator, state)
	if err != nil {
		return stats, err
	}

	_, err = s.db.ExecContext(ctx, `UPDATE libraries SET last_scan_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, library.ID)
	if err != nil {
		return stats, fmt.Errorf("update library last_scan_at: %w", err)
	}
	return stats, nil
}

func mapKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func appendUniqueStrings(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	for _, item := range base {
		seen[item] = struct{}{}
	}
	for _, item := range extra {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		base = append(base, item)
	}
	return base
}

func (s *Scanner) pruneLibrary(ctx context.Context, library Library, accumulator *scanAccumulator) (ScanStats, error) {
	if strings.HasPrefix(library.Path, "samo://") {
		return ScanStats{}, nil
	}
	if len(accumulator.filePaths) == 0 {
		existing, err := s.countIndexedPaths(ctx, library.ID)
		if err != nil {
			return ScanStats{}, err
		}
		if existing > 0 {
			log.Printf("scanner: skip prune for library %q — walk found 0 files but %d indexed path(s) remain (check mount, .ndignore, or library path)", library.Name, existing)
			return ScanStats{}, nil
		}
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

	filePruned, fileMarked, err := s.pruneMediaFiles(ctx, library.ID, accumulator.filePaths)
	if err != nil {
		return stats, err
	}
	stats.FilesPruned = filePruned
	stats.FilesMarked = fileMarked
	return stats, nil
}

func (s *Scanner) countIndexedPaths(ctx context.Context, libraryID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_files WHERE library_id = ?`, libraryID).Scan(&count)
	return count, err
}

func (s *Scanner) pruneMediaFiles(ctx context.Context, libraryID string, seenPaths map[string]struct{}) (int, int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM media_files WHERE library_id = ? AND missing = 0`, libraryID)
	if err != nil {
		return 0, 0, fmt.Errorf("list media files for prune: %w", err)
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return 0, 0, fmt.Errorf("scan media file path: %w", err)
		}
		if _, ok := seenPaths[path]; !ok {
			stale = append(stale, path)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	pruned := 0
	marked := 0
	for index, path := range stale {
		if index > 0 && index%500 == 0 {
			log.Printf("scanner: prune-check library=%q checked=%d/%d pruned=%d marked=%d", libraryID, index, len(stale), pruned, marked)
			if s.onActivity != nil {
				s.onActivity(fmt.Sprintf("pruning stale files… (%d/%d)", index, len(stale)))
			}
		}
		if fileReachable(ctx, path) {
			if _, err := s.db.ExecContext(ctx, `DELETE FROM media_files WHERE library_id = ? AND path = ?`, libraryID, path); err != nil {
				return pruned, marked, fmt.Errorf("delete stale media file %q: %w", path, err)
			}
			pruned++
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE media_files
			SET missing = 1,
			    missing_detected_at = COALESCE(missing_detected_at, CURRENT_TIMESTAMP),
			    updated_at = CURRENT_TIMESTAMP
			WHERE library_id = ? AND path = ?`, libraryID, path); err != nil {
			return pruned, marked, fmt.Errorf("mark missing media file %q: %w", path, err)
		}
		marked++
	}
	return pruned, marked, nil
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

func (s *Scanner) pruneOrphanMusic(ctx context.Context) (int, error) {
	statements := []string{
		`DELETE FROM music_tracks
		 WHERE id NOT IN (SELECT track_id FROM media_files WHERE track_id IS NOT NULL)
		   AND id NOT IN (
		     SELECT DISTINCT j.value
		     FROM music_playlists p, json_each(p.track_ids_json) AS j
		     WHERE j.value IS NOT NULL AND TRIM(j.value) != ''
		   )`,
		`DELETE FROM music_albums
		 WHERE id NOT IN (SELECT album_id FROM music_tracks WHERE album_id IS NOT NULL)`,
		`DELETE FROM music_artists
		 WHERE id NOT IN (SELECT artist_id FROM music_track_artists)
		   AND id NOT IN (SELECT artist_id FROM music_album_artists)`,
	}
	pruned := 0
	for _, statement := range statements {
		res, err := s.db.ExecContext(ctx, statement)
		if err != nil {
			return pruned, fmt.Errorf("prune orphan music rows: %w", err)
		}
		if rows, err := res.RowsAffected(); err == nil {
			pruned += int(rows)
		}
	}
	return pruned, nil
}
