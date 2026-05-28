package scanner

import (
	"context"
	"log"
)

// runPhaseFolders scans music files with Navidrome-style per-album-folder
// change detection. Non-music libraries keep the existing per-file walk.
func (s *Scanner) runPhaseFolders(ctx context.Context, library Library, root string, files []string, state *scanState) error {
	kind := library.Kind
	if kind == "mixed" {
		return s.scanMixedLibrary(ctx, library, root, files, state)
	}
	if kind != "music" {
		return s.scanLibraryLegacy(ctx, library, root, files)
	}
	return s.scanMusicLibraryByFolder(ctx, library, root, files, state)
}

func (s *Scanner) scanLibraryLegacy(ctx context.Context, library Library, root string, files []string) error {
	kind := library.Kind
	switch kind {
	case "audiobook":
		return s.scanAudiobookLibrary(ctx, library, root, files)
	case "podcast":
		return s.scanPodcastLibrary(ctx, library, root, files)
	default:
		for index, path := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if index > 0 && index%500 == 0 {
				log.Printf("scanner: music library %q progress %d/%d", library.Name, index, len(files))
			}
			if err := s.scanMusicFile(ctx, library, root, path); err != nil {
				return err
			}
		}
		return nil
	}
}

func (s *Scanner) scanMusicLibraryByFolder(ctx context.Context, library Library, root string, files []string, state *scanState) error {
	fullScan := state.fullScan || s.scanMode == ScanModeFull || s.scanMode == ScanModeRepair
	prevHashes, err := s.loadFolderHashes(ctx, library.ID)
	if err != nil {
		return err
	}

	folders := groupFilesByAlbumFolder(root, files)
	seenFolders := map[string]struct{}{}
	for index, folder := range folders {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := folderSeenKey(folder.relPath)
		seenFolders[key] = struct{}{}
		folder.prevHash = prevHashes[key]

		if !folder.isOutdated(fullScan) {
			for _, path := range folder.files {
				if s.markIndexedFileSeen(path) {
					continue
				}
				s.activeScan.seeFile(path)
			}
			continue
		}

		if index > 0 && index%100 == 0 {
			log.Printf("scanner: music library %q folder progress %d/%d", library.Name, index, len(folders))
		}
		for _, path := range folder.files {
			if err := s.scanMusicFile(ctx, library, root, path); err != nil {
				return err
			}
		}
		state.noteChange()
		if err := s.saveFolderHash(ctx, library.ID, folder); err != nil {
			return err
		}
	}

	if len(state.subpaths) == 0 {
		return s.markMissingFolders(ctx, library.ID, seenFolders)
	}
	return nil
}
