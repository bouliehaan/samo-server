package api

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/log"
)

func (s *Server) serveMusicPlaylistCover(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if _, err := s.catalog.MusicPlaylistForUser(principal.User.ID, id); err != nil {
		writeCatalogError(w, err)
		return
	}

	// This route names a playlist, whose source artwork can change without a
	// playlist edit (metadata repair/download). It is not an immutable image ID.
	w.Header().Set("Cache-Control", "private, no-cache")
	images := s.catalog.MusicPlaylistCoverImages(id)
	wantsGrid := len(images) == 4
	hashParts, sourcePaths := s.playlistCoverCompositeSources(r, images)
	if len(sourcePaths) > 0 {
		// Single covers and a failed compositor need the repaired local paths
		// too; falling back to the original stale records would return a 404.
		images = make([]catalog.Image, len(sourcePaths))
		for i, path := range sourcePaths {
			images[i] = catalog.Image{ID: hashParts[i], Path: path}
		}
	} else {
		// A remote cover may still be usable by the client if its download
		// failed here. Never let an obsolete local path hide that fallback.
		for i := range images {
			images[i].Path = ""
		}
	}
	if wantsGrid {
		if len(sourcePaths) == 4 {
			composite, err := s.coversService().Composite(r.Context(), id, strings.Join(hashParts, ","), sourcePaths)
			if err == nil {
				images = []catalog.Image{*composite}
			} else {
				log.Warnf("playlist cover %s: 2x2 composite failed, serving single cover: %v", id, err)
			}
		} else {
			log.Infof("playlist cover %s: %d/4 servable sources, serving single cover", id, len(sourcePaths))
		}
	}

	s.serveCatalogImage(w, r, images)
}

func (s *Server) playlistCoverCompositeSources(r *http.Request, images []catalog.Image) ([]string, []string) {
	hashParts := make([]string, 0, len(images))
	sourcePaths := make([]string, 0, len(images))

	for _, img := range images {
		// Stored paths can be stale and URL is often just provenance. Resolve
		// the ID independently before asking ffmpeg to open a remote source.
		candidates := []catalog.Image{img}
		if resolved, ok := s.resolveCatalogImageRecord(r.Context(), []catalog.Image{{ID: img.ID}}); ok {
			candidates = append(candidates, resolved)
		}
		path := ""
		for _, candidate := range candidates {
			if info, err := os.Stat(candidate.Path); err == nil && !info.IsDir() {
				path = candidate.Path
				break
			}
		}
		if path == "" {
			for _, candidate := range candidates {
				if strings.TrimSpace(candidate.URL) == "" {
					continue
				}
				// Use the bounded, cached cover downloader; ffmpeg should only
				// see local image files, not redirects or remote HTTP failures.
				if downloaded, err := s.coversService().DownloadFromURL(r.Context(), candidate.URL); err == nil {
					path = downloaded.Path
					break
				}
			}
		}
		if path == "" {
			continue
		}
		hashID := strings.TrimSpace(img.ID)
		if hashID == "" {
			hashID = path
		}
		hashParts = append(hashParts, hashID)
		sourcePaths = append(sourcePaths, path)
	}

	return hashParts, sourcePaths
}

func (s *Server) uploadMusicPlaylistCover(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not configured")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "playlist id is required")
		return
	}
	playlist, err := s.catalog.MusicPlaylistForUser(principal.User.ID, id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	if playlist.OwnerID != "" && playlist.OwnerID != principal.User.ID && principal.User.Role != "admin" {
		writeError(w, http.StatusForbidden, "playlist owner required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	if err := r.ParseMultipartForm(6 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("cover")
	if err != nil {
		writeError(w, http.StatusBadRequest, "cover file is required")
		return
	}
	defer file.Close()

	contentType := ""
	if header != nil {
		contentType = header.Header.Get("Content-Type")
	}
	image, err := s.coversService().StoreFromUpload(r.Context(), "music-playlist:"+id, contentType, file)
	if err != nil {
		writeCoverUploadError(w, err)
		return
	}
	if err := catalogstore.SetMusicPlaylistCover(r.Context(), s.db, id, *image); err != nil {
		writeCatalogDeleteError(w, err)
		return
	}
	// Read the row back from the database, not the projection: the projection
	// is what is about to be updated FROM it, so asking it now would return the
	// playlist without its new cover.
	updated, err := s.playlistsService().Get(r.Context(), id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	if !s.commitPlaylist(w, r, updated) {
		return
	}

	item, err := s.catalog.MusicPlaylistForUser(principal.User.ID, id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       item.ID,
		"images":   item.Images,
		"coverUrl": publicURL(r, "/api/v1/music/playlists/"+url.PathEscape(id)+"/cover"),
	})
}
