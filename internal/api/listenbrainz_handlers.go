package api

import (
	"errors"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/listenbrainz"
)

func (s *Server) getListenBrainzStatus(w http.ResponseWriter, r *http.Request) {
	service := s.listenbrainzService()
	if service == nil {
		writeJSON(w, http.StatusOK, listenbrainz.Status{Enabled: false})
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

// connectListenBrainz stores a user's personal token. There is no application
// key or OAuth round trip to make first — the token IS the connection.
func (s *Server) connectListenBrainz(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireListenBrainz(w)
	if !ok {
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input listenbrainz.ConnectInput
	if !readJSONBody(w, r, &input) {
		return
	}
	response, err := service.Connect(r.Context(), principal.User.ID, input)
	if err != nil {
		writeListenBrainzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) disconnectListenBrainz(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireListenBrainz(w)
	if !ok {
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := service.Disconnect(r.Context(), principal.User.ID); err != nil {
		writeListenBrainzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"disconnected": true})
}

func (s *Server) flushListenBrainzQueue(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireListenBrainz(w)
	if !ok {
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// An explicit retry ignores the backoff schedule — the caller is asserting
	// that whatever was failing has been fixed.
	flushed, err := service.RetryQueue(r.Context(), principal.User.ID, 100)
	if err != nil {
		writeListenBrainzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"flushed": flushed})
}

func (s *Server) listListenBrainzQueue(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireListenBrainz(w)
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
		writeListenBrainzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listListenBrainzHistory(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireListenBrainz(w)
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
		writeListenBrainzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listenbrainzService() *listenbrainz.Service {
	return s.listenbrainz
}

func (s *Server) requireListenBrainz(w http.ResponseWriter) (*listenbrainz.Service, bool) {
	service := s.listenbrainzService()
	if service == nil || !service.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "listenbrainz integration is not available")
		return nil, false
	}
	return service, true
}

func writeListenBrainzError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, listenbrainz.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, listenbrainz.ErrNotConnected):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, listenbrainz.ErrMissingToken):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, listenbrainz.ErrInvalidToken):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, listenbrainz.ErrInvalidRoot):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, listenbrainz.ErrMissingMetadata):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, listenbrainz.ErrInvalidEvent):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}
