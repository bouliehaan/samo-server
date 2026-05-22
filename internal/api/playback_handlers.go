package api

import (
	"errors"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

func (s *Server) getPlayback(w http.ResponseWriter, r *http.Request) {
	kind, err := playback.ParseTargetKind(r.PathValue("kind"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	state, err := s.playbackService().Get(r.Context(), kind, r.PathValue("id"))
	if err != nil {
		writePlaybackError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) putPlayback(w http.ResponseWriter, r *http.Request) {
	kind, err := playback.ParseTargetKind(r.PathValue("kind"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var state catalog.PlaybackState
	if !readJSONBody(w, r, &state) {
		return
	}
	updated, err := s.playbackService().Put(r.Context(), kind, r.PathValue("id"), state)
	if err != nil {
		writePlaybackError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) patchPlayback(w http.ResponseWriter, r *http.Request) {
	kind, err := playback.ParseTargetKind(r.PathValue("kind"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var patch playback.PatchInput
	if !readJSONBody(w, r, &patch) {
		return
	}
	updated, err := s.playbackService().Patch(r.Context(), kind, r.PathValue("id"), patch)
	if err != nil {
		writePlaybackError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) playbackService() *playback.Service {
	if s.playback == nil {
		panic("playback service is not configured")
	}
	return s.playback
}

func writePlaybackError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, playback.ErrNotFound):
		writeError(w, http.StatusNotFound, "playback target not found")
	case errors.Is(err, playback.ErrInvalidTarget):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, playback.ErrInvalidState):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
