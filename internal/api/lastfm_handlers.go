package api

import (
	"errors"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/lastfm"
)

func (s *Server) getLastFMStatus(w http.ResponseWriter, r *http.Request) {
	service := s.lastfmService()
	if service == nil {
		writeJSON(w, http.StatusOK, lastfm.Status{Enabled: false})
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	status, err := service.Status(r.Context(), principal.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) beginLastFMAuth(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireLastFM(w)
	if !ok {
		return
	}
	response, err := service.BeginAuth(r.Context())
	if err != nil {
		writeLastFMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) completeLastFMAuth(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireLastFM(w)
	if !ok {
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input lastfm.AuthCompleteInput
	if !readJSONBody(w, r, &input) {
		return
	}
	response, err := service.CompleteAuth(r.Context(), principal.User.ID, input.Token)
	if err != nil {
		writeLastFMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) disconnectLastFM(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireLastFM(w)
	if !ok {
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := service.Disconnect(r.Context(), principal.User.ID); err != nil {
		writeLastFMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"disconnected": true})
}

func (s *Server) flushLastFMQueue(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireLastFM(w)
	if !ok {
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	flushed, err := service.FlushQueue(r.Context(), principal.User.ID, 50)
	if err != nil {
		writeLastFMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"flushed": flushed})
}

func (s *Server) listLastFMQueue(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireLastFM(w)
	if !ok {
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := service.ListQueue(r.Context(), principal.User.ID, page.Limit, page.Offset)
	if err != nil {
		writeLastFMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listLastFMHistory(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireLastFM(w)
	if !ok {
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := service.ListHistory(r.Context(), principal.User.ID, page.Limit, page.Offset)
	if err != nil {
		writeLastFMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) lastfmService() *lastfm.Service {
	return s.lastfm
}

func (s *Server) requireLastFM(w http.ResponseWriter) (*lastfm.Service, bool) {
	service := s.lastfmService()
	if service == nil || !service.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "last.fm integration is not configured")
		return nil, false
	}
	return service, true
}

func writeLastFMError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lastfm.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, lastfm.ErrNotConnected):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, lastfm.ErrInvalidToken):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, lastfm.ErrMissingMetadata):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, lastfm.ErrInvalidEvent):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}
