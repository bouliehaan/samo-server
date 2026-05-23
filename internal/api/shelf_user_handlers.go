package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/shelfuser"
)

func (s *Server) listShelfItemBookmarks(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := s.shelfUserService().ListBookmarks(r.Context(), principal.User.ID, r.PathValue("id"))
	if err != nil {
		writeShelfUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createShelfItemBookmark(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input shelfuser.CreateBookmarkInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.shelfUserService().CreateBookmark(r.Context(), principal.User.ID, r.PathValue("id"), input)
	if err != nil {
		writeShelfUserError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateShelfBookmark(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input shelfuser.UpdateBookmarkInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.shelfUserService().UpdateBookmark(r.Context(), principal.User.ID, r.PathValue("id"), input)
	if err != nil {
		writeShelfUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteShelfBookmark(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.shelfUserService().DeleteBookmark(r.Context(), principal.User.ID, r.PathValue("id")); err != nil {
		writeShelfUserError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listShelfCollections(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := s.shelfUserService().ListCollections(r.Context(), principal.User.ID)
	if err != nil {
		writeShelfUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createShelfCollection(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input shelfuser.CreateCollectionInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.shelfUserService().CreateCollection(r.Context(), principal.User.ID, input)
	if err != nil {
		writeShelfUserError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getShelfCollection(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	item, err := s.shelfUserService().GetCollection(r.Context(), principal.User.ID, r.PathValue("id"))
	if err != nil {
		writeShelfUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateShelfCollection(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input shelfuser.UpdateCollectionInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.shelfUserService().UpdateCollection(r.Context(), principal.User.ID, r.PathValue("id"), input)
	if err != nil {
		writeShelfUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteShelfCollection(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.shelfUserService().DeleteCollection(r.Context(), principal.User.ID, r.PathValue("id")); err != nil {
		writeShelfUserError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listShelfItemSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	limit := sessionLimit(r)
	items, err := s.shelfUserService().ListSessionsForItem(r.Context(), principal.User.ID, r.PathValue("id"), limit)
	if err != nil {
		writeShelfUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listShelfListeningSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	limit := sessionLimit(r)
	items, err := s.shelfUserService().ListRecentSessions(r.Context(), principal.User.ID, limit)
	if err != nil {
		writeShelfUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listShelfAuthorItems(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.catalog.ShelfItemsForAuthor(r.PathValue("id"), page)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listShelfSeriesItems(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.catalog.ShelfItemsForSeries(r.PathValue("id"), page)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) shelfUserService() *shelfuser.Service {
	if s.shelfUser == nil {
		panic("shelf user service is not configured")
	}
	return s.shelfUser
}

func writeShelfUserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, shelfuser.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, shelfuser.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, shelfuser.ErrInvalidInput), errors.Is(err, shelfuser.ErrNotAudiobook):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, shelfuser.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func sessionLimit(r *http.Request) int {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 500 {
		limit = 500
	}
	return limit
}

func (s *Server) recordShelfListeningSession(ctx context.Context, userID string, itemID string, before, after catalog.PlaybackState, patch *playback.PatchInput) {
	if s.shelfUser == nil {
		return
	}
	start := before.ProgressSeconds
	end := after.ProgressSeconds
	if patch != nil && patch.ProgressSeconds != nil {
		end = *patch.ProgressSeconds
	}
	if patch != nil && patch.PlayCount != nil && *patch.PlayCount > before.PlayCount && end == start {
		if end == 0 {
			end = start + 30
		}
	}
	if start == end && (patch == nil || (patch.ProgressSeconds == nil && patch.Completed == nil && patch.PlayCount == nil)) {
		return
	}
	_, _ = s.shelfUser.RecordSession(ctx, userID, shelfuser.RecordSessionInput{
		ItemID:               itemID,
		StartPositionSeconds: start,
		EndPositionSeconds:   end,
		Completed:            after.Completed,
	})
}
