package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/wrapped"
)

// A Wrapped reads its music straight from the Last.fm integration.
var _ wrapped.LastFM = (*lastfm.Service)(nil)

// wrappedResponse is a Wrapped, or how far along reading it from Last.fm is.
type wrappedResponse struct {
	*wrapped.Report
	Building *wrapped.Building `json:"building,omitempty"`
}

// getWrapped is the signed-in listener's year: GET /api/v1/wrapped?year=2026.
// 202 while it is still being read from Last.fm; ask again. 404 with
// code "no-source" when there is nothing to build it from.
func (s *Server) getWrapped(w http.ResponseWriter, r *http.Request) {
	if s.wrapped == nil {
		writeError(w, http.StatusServiceUnavailable, "wrapped is unavailable")
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	year := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("year")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "year must be a number")
			return
		}
		year = parsed
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	report, building, err := s.wrapped.Report(r.Context(), principal.User.ID, year, refresh)
	switch {
	case wrapped.IsNoSource(err):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "no-source", "error": err.Error()})
	case err == wrapped.ErrBadYear:
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
	case building != nil:
		writeJSON(w, http.StatusAccepted, wrappedResponse{Building: building})
	default:
		writeJSON(w, http.StatusOK, wrappedResponse{Report: report})
	}
}

func (s *Server) getWrappedSettings(w http.ResponseWriter, r *http.Request) {
	if s.wrapped == nil {
		writeError(w, http.StatusServiceUnavailable, "wrapped is unavailable")
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	settings, err := s.wrapped.Settings(r.Context(), principal.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// putWrappedSettings turns samo's own listening history on or off for the
// signed-in listener: {"localHistory": true}. Each listener decides for
// themselves; nobody else's switch, admin or not, is reachable here.
func (s *Server) putWrappedSettings(w http.ResponseWriter, r *http.Request) {
	if s.wrapped == nil {
		writeError(w, http.StatusServiceUnavailable, "wrapped is unavailable")
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input struct {
		LocalHistory *bool `json:"localHistory"`
	}
	if !readJSONBody(w, r, &input) {
		return
	}
	if input.LocalHistory == nil {
		writeError(w, http.StatusBadRequest, "localHistory is required")
		return
	}
	settings, err := s.wrapped.SetLocalHistory(r.Context(), principal.User.ID, *input.LocalHistory)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// deleteWrappedHistory erases the listening samo has logged for the
// signed-in listener. The switch stays where it is.
func (s *Server) deleteWrappedHistory(w http.ResponseWriter, r *http.Request) {
	if s.wrapped == nil {
		writeError(w, http.StatusServiceUnavailable, "wrapped is unavailable")
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	settings, err := s.wrapped.DeleteHistory(r.Context(), principal.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// wrappedPage is the Wrapped itself. The shell is public like /login: the
// page signs in with the token the player keeps, and every number on it
// comes from the authenticated API.
func (s *Server) wrappedPage(w http.ResponseWriter, r *http.Request) {
	// Covers from samo's authenticated routes are fetched and drawn from blobs.
	w.Header().Set("Content-Security-Policy", strings.Replace(contentSecurityPolicy, "img-src 'self' data:", "img-src 'self' blob: data:", 1))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(wrappedPageBytes)
}

var wrappedPageBytes = []byte(pageSource("wrapped"))
