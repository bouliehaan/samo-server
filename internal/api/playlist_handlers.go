package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bouliehaan/samo-server/internal/playlists"
)

func (s *Server) createMusicPlaylist(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input playlists.CreateInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.playlistsService().Create(r.Context(), principal.User.ID, input)
	if err != nil {
		writePlaylistError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) importMusicPlaylist(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input playlists.ImportInput
	if !readJSONBody(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.URL) != "" && principal.User.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin required for server-side playlist url imports")
		return
	}
	result, err := s.playlistsService().Import(r.Context(), principal.User.ID, input)
	if err != nil {
		writePlaylistError(w, err)
		return
	}
	if !input.DryRun {
		if err := s.reloadCatalogProjection(r); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listMusicPlaylistTracks(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if _, err := s.catalog.MusicPlaylistForUser(principal.User.ID, r.PathValue("id")); err != nil {
		writeCatalogError(w, err)
		return
	}
	items := s.catalog.MusicTracksForPlaylist(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) updateMusicPlaylist(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input playlists.UpdateInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.playlistsService().Update(r.Context(), principal.User.ID, r.PathValue("id"), input)
	if err != nil {
		writePlaylistError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteMusicPlaylist(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.playlistsService().Delete(r.Context(), principal.User.ID, r.PathValue("id")); err != nil {
		writePlaylistError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) playlistsService() *playlists.Service {
	if s.playlists == nil {
		panic("playlist service is not configured")
	}
	return s.playlists
}

func writePlaylistError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, playlists.ErrNotFound):
		writeError(w, http.StatusNotFound, "playlist not found")
	case errors.Is(err, playlists.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, playlists.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, playlists.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
