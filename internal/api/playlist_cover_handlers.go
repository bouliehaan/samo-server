package api

import (
	"net/http"
	"net/url"
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

	images := s.catalog.MusicPlaylistCoverImages(id)
	if len(images) == 4 {
		hashParts, sourcePaths := s.playlistCoverCompositeSources(r, images)
		if len(sourcePaths) == 4 {
			imagesHash := strings.Join(hashParts, ",")
			composite, err := s.coversService().Composite(r.Context(), id, imagesHash, sourcePaths)
			if err == nil {
				images = []catalog.Image{*composite}
			} else {
				// The 2x2 grid failed to render (a common cause is ffmpeg
				// choking on a cover that is still a remote URL instead of a
				// downloaded local file). We deliberately fall through and serve
				// the first cover rather than error the request — but log it, so
				// the degrade from grid to single tile is diagnosable instead of
				// silent.
				log.Warnf("playlist cover %s: 2x2 composite failed, serving single cover: %v", id, err)
			}
		} else {
			// Fewer than 4 servable sources (e.g. covers not yet backfilled)
			// means no grid is possible; serving one cover is expected here, but
			// log it so "the Explore tile isn't a grid" is explainable.
			log.Infof("playlist cover %s: %d/4 servable sources, serving single cover", id, len(sourcePaths))
		}
	}

	s.serveCatalogImage(w, r, images)
}

func (s *Server) playlistCoverCompositeSources(r *http.Request, images []catalog.Image) ([]string, []string) {
	hashParts := make([]string, 0, len(images))
	sourcePaths := make([]string, 0, len(images))

	for _, img := range images {
		path := strings.TrimSpace(img.Path)
		if path == "" {
			path = strings.TrimSpace(img.URL)
		}

		hashID := strings.TrimSpace(img.ID)
		if path == "" {
			if resolved, ok := s.resolveCatalogImageRecord(r.Context(), []catalog.Image{img}); ok {
				path = strings.TrimSpace(resolved.Path)
				if path == "" {
					path = strings.TrimSpace(resolved.URL)
				}
				if resolvedID := strings.TrimSpace(resolved.ID); resolvedID != "" {
					hashID = resolvedID
				}
			}
		}

		if path == "" {
			continue
		}
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
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
