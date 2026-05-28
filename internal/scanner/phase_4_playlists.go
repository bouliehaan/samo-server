package scanner

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// PlaylistImporter imports on-disk M3U playlists during phase 4.
type PlaylistImporter interface {
	ImportM3UFromPath(ctx context.Context, ownerID, path string) (bool, error)
	FirstAdminOwnerID(ctx context.Context) (string, error)
}

func (s *Scanner) runPhasePlaylists(ctx context.Context, library Library, root string, state *scanState) error {
	if !s.autoImportPlaylists || s.playlistImport == nil {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(library.Kind))
	if kind != "music" && kind != "mixed" {
		return nil
	}
	ownerID, err := s.playlistImport.FirstAdminOwnerID(ctx)
	if err != nil {
		log.Printf("scanner: skip playlist import for %q (no admin user: %v)", library.Name, err)
		return nil
	}

	imported := 0
	err = walkLibraryDir(ctx, root, func(path string, entry os.DirEntry) error {
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".m3u" && ext != ".m3u8" {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		ok, err := s.playlistImport.ImportM3UFromPath(ctx, ownerID, path)
		if err != nil {
			log.Printf("scanner: playlist import %q: %v", path, err)
			return nil
		}
		if ok {
			imported++
			state.noteChange()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if imported > 0 {
		log.Printf("scanner: imported %d playlist(s) from library %q", imported, library.Name)
	}
	return nil
}
