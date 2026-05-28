package scanner

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"
)

// runLibraryPipeline executes Navidrome's scan phases for one library:
//  1. Folder scan (import / update files)
//  2. Missing tracks (moved-file reconciliation)
//  3. Refresh albums (aggregate metadata from tracks)
//  4. Prune stale rows (scoped to this library)
func (s *Scanner) runLibraryPipeline(
	ctx context.Context,
	library Library,
	accumulator *scanAccumulator,
	state *scanState,
) (ScanStats, error) {
	root, err := filepath.Abs(strings.TrimSpace(library.Path))
	if err != nil {
		return ScanStats{}, err
	}
	if err := s.upsertLibrary(ctx, library); err != nil {
		return ScanStats{}, err
	}

	if s.onActivity != nil {
		s.onActivity(fmt.Sprintf("walking %q", library.Name))
	}
	files, err := audioFiles(ctx, root, s.onWalkProgress)
	if err != nil {
		return ScanStats{}, err
	}
	files = filterFilesUnderSubpaths(files, state.subpaths)

	kind := strings.ToLower(strings.TrimSpace(library.Kind))
	if s.onActivity != nil {
		s.onActivity(fmt.Sprintf("indexing %q (%d files)", library.Name, len(files)))
	}
	log.Printf("scanner: phase 1 folders library %q kind=%q mode=%q path=%q files=%d",
		library.Name, kind, s.scanMode, library.Path, len(files))

	start := time.Now()
	if err := s.runPhaseFolders(ctx, library, root, files, state); err != nil {
		return ScanStats{}, err
	}
	log.Printf("scanner: phase 1 done library %q in %s", library.Name, time.Since(start))

	if len(state.subpaths) == 0 && (library.Kind == "music" || library.Kind == "mixed") {
		if s.onActivity != nil {
			s.onActivity(fmt.Sprintf("checking moved files in %q…", library.Name))
		}
		if _, err := s.markUnseenMediaFilesMissing(ctx, library.ID, accumulator.filePaths); err != nil {
			return ScanStats{}, err
		}
	}

	if err := s.runPhaseMissingTracks(ctx, library, state); err != nil {
		return ScanStats{}, err
	}

	if s.onActivity != nil {
		switch library.Kind {
		case "music", "mixed":
			s.onActivity(fmt.Sprintf("refreshing albums in %q…", library.Name))
		default:
			s.onActivity(fmt.Sprintf("finishing %q…", library.Name))
		}
	}
	if err := s.runParallelRefreshAndPlaylists(ctx, library, root, accumulator, state); err != nil {
		return ScanStats{}, err
	}

	stats := ScanStats{
		FilesSeen:    len(accumulator.filePaths),
		NewArtistIDs: mapKeys(accumulator.newArtistIDs),
	}
	if len(state.subpaths) > 0 {
		return stats, nil
	}

	pruneStats, err := s.pruneLibrary(ctx, library, accumulator)
	stats.FilesPruned = pruneStats.FilesPruned
	stats.FilesMarked = pruneStats.FilesMarked
	stats.ItemsPruned = pruneStats.ItemsPruned
	return stats, err
}
