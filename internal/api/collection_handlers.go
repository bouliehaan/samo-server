package api

import (
	"net/http"

	"github.com/bouliehaan/samo-server/internal/bookmarks"
)

func (s *Server) listCollections(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := s.bookmarksService().ListCollections(r.Context(), principal.User.ID)
	if err != nil {
		writeBookmarksError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createCollection(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input bookmarks.CreateCollectionInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.bookmarksService().CreateCollection(r.Context(), principal.User.ID, input)
	if err != nil {
		writeBookmarksError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getCollection(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	item, err := s.bookmarksService().GetCollection(r.Context(), principal.User.ID, r.PathValue("id"))
	if err != nil {
		writeBookmarksError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateCollection(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input bookmarks.UpdateCollectionInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.bookmarksService().UpdateCollection(r.Context(), principal.User.ID, r.PathValue("id"), input)
	if err != nil {
		writeBookmarksError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteCollection(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.bookmarksService().DeleteCollection(r.Context(), principal.User.ID, r.PathValue("id")); err != nil {
		writeBookmarksError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
