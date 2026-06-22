package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
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
		var hashParts []string
		var sourcePaths []string
		for _, img := range images {
			path := img.Path
			if path == "" {
				path = img.URL
			}
			hashID := img.ID
			if path == "" {
				resolved, ok := s.resolveCatalogImageRecord(r.Context(), []catalog.Image{img})
				if ok {
					path = resolved.Path
					if path == "" {
						path = resolved.URL
					}
					if resolved.ID != "" {
						hashID = resolved.ID
					}
				}
			}
			if path != "" {
				sourcePaths = append(sourcePaths, path)
				if hashID == "" {
					hashID = path
				}
				hashParts = append(hashParts, hashID)
			}
		}
		if len(sourcePaths) == 4 {
			imagesHash := strings.Join(hashParts, ",")
			composite, err := s.coversService().Composite(r.Context(), id, imagesHash, sourcePaths)
			if err == nil {
				images = []catalog.Image{*composite}
			} else {
				// ADDED LOGGING FOR DEBUGGING
				println("serveMusicPlaylistCover: Composite failed for playlist", id, "error:", err.Error())
				for i, p := range sourcePaths {
					println("  source", i, p)
				}
			}
		} else {
			println("serveMusicPlaylistCover: len(sourcePaths) is", len(sourcePaths), "expected 4 for playlist", id)
		}
	} else {
		println("serveMusicPlaylistCover: len(images) is", len(images), "expected 4 for playlist", id)
	}

	s.serveCatalogImage(w, r, images)
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
	if err := catalog.SetMusicPlaylistCover(r.Context(), s.db, id, *image); err != nil {
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
