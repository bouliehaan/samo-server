package api

import (
	"errors"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/covers"
)

func (s *Server) getExtractedCover(w http.ResponseWriter, r *http.Request) {
	image, err := s.coversService().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeCoverError(w, err)
		return
	}
	if image.Path == "" {
		writeError(w, http.StatusNotFound, "cover not found")
		return
	}
	if err := s.filesService().ServeLocalPath(r.Context(), image.Path, w, r); err != nil {
		writeFilesError(w, err)
	}
}

func (s *Server) serveExtractedCover(w http.ResponseWriter, r *http.Request) {
	s.getExtractedCover(w, r)
}

func (s *Server) coversService() *covers.Service {
	if s.covers == nil {
		panic("covers service is not configured")
	}
	return s.covers
}

func writeCoverError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, covers.ErrNotFound):
		writeError(w, http.StatusNotFound, "cover not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
