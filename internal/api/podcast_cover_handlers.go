package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func (s *Server) uploadPodcastCover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not configured")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "podcast id is required")
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
	image, err := s.coversService().StoreFromUpload(r.Context(), "podcast:"+id, contentType, file)
	if err != nil {
		writeCoverUploadError(w, err)
		return
	}
	if err := catalog.SetPodcastCover(r.Context(), s.db, id, *image); err != nil {
		writeCatalogDeleteError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	item, err := s.catalog.Podcast(id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       item.ID,
		"cover":    item.Cover,
		"coverUrl": publicURL(r, "/api/v1/podcasts/shows/"+url.PathEscape(id)+"/cover"),
	})
}
