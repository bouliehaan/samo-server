package scanner

import (
	"context"
	"strings"
)

func (s *Scanner) loadFolderHashes(ctx context.Context, libraryID string) (map[string]string, error) {
	return s.store.FolderHashes(ctx, libraryID)
}

// saveFolderHash records the folder's content hash. hash() is derived from the
// folder's entries, so it is computed here and the result handed down.
func (s *Scanner) saveFolderHash(ctx context.Context, libraryID string, folder albumFolder) error {
	return s.store.SaveFolderHash(ctx, libraryID, folder.relPath, folder.hash(), folder.modTime)
}

// markMissingFolders forgets folders no longer on disk, so a later scan treats
// their paths as new rather than as unchanged-since-last-time.
//
// A delete failure is ignored: a stale hash costs one redundant folder walk
// next scan, which is not worth failing a scan over.
func (s *Scanner) markMissingFolders(ctx context.Context, libraryID string, seenFolders map[string]struct{}) error {
	paths, err := s.store.FolderPaths(ctx, libraryID)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if _, ok := seenFolders[path]; !ok {
			_ = s.store.DeleteFolderHash(ctx, libraryID, path)
		}
	}
	return nil
}

func folderSeenKey(relPath string) string {
	return strings.TrimSpace(relPath)
}
