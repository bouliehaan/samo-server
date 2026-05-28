package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bouliehaan/samo-server/internal/artistimages"
)

func (s *Server) startArtistImageBackfill(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.artistImages == nil || !s.artistImages.Enabled() {
		writeError(w, http.StatusServiceUnavailable, artistimages.ErrBackfillNotAvailable.Error())
		return
	}
	var input struct {
		Mode string `json:"mode"`
	}
	_ = decodeJSONOptional(r, &input)
	result, err := s.artistImages.StartBackfill(r.Context(), strings.TrimSpace(input.Mode))
	if err != nil {
		writeArtistImageBackfillError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) getArtistImageBackfill(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.artistImages == nil {
		writeJSON(w, http.StatusOK, map[string]any{"job": nil})
		return
	}
	job, ok := s.artistImages.GetBackfillJob()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"job": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) cancelArtistImageBackfill(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.artistImages == nil {
		writeError(w, http.StatusServiceUnavailable, artistimages.ErrBackfillNotAvailable.Error())
		return
	}
	job, err := s.artistImages.CancelBackfill()
	if err != nil {
		writeArtistImageBackfillError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func writeArtistImageBackfillError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, artistimages.ErrBackfillNotRunning):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, artistimages.ErrBackfillNotAvailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
