package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/bouliehaan/samo-server/internal/bookmarks"
	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

func (s *Server) listAudiobookBookmarks(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := s.bookmarksService().ListBookmarks(r.Context(), principal.User.ID, r.PathValue("id"))
	if err != nil {
		writeBookmarksError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createAudiobookBookmark(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input bookmarks.CreateBookmarkInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.bookmarksService().CreateBookmark(r.Context(), principal.User.ID, r.PathValue("id"), input)
	if err != nil {
		writeBookmarksError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateBookmark(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input bookmarks.UpdateBookmarkInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.bookmarksService().UpdateBookmark(r.Context(), principal.User.ID, r.PathValue("id"), input)
	if err != nil {
		writeBookmarksError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteBookmark(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.bookmarksService().DeleteBookmark(r.Context(), principal.User.ID, r.PathValue("id")); err != nil {
		writeBookmarksError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAudiobookSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	limit := sessionLimit(r)
	items, err := s.bookmarksService().ListSessionsForAudiobook(r.Context(), principal.User.ID, r.PathValue("id"), limit)
	if err != nil {
		writeBookmarksError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listListeningSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	limit := sessionLimit(r)
	items, err := s.bookmarksService().ListRecentSessions(r.Context(), principal.User.ID, limit)
	if err != nil {
		writeBookmarksError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// listUserBookmarks returns every bookmark the current user has saved
// across all audiobooks. Distinct from listListeningSessions — bookmarks
// are user-placed markers, sessions are playback-derived analytics rows.
func (s *Server) listUserBookmarks(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := s.bookmarksService().ListUserBookmarks(r.Context(), principal.User.ID, sessionLimit(r))
	if err != nil {
		writeBookmarksError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) bookmarksService() *bookmarks.Service {
	if s.bookmarks == nil {
		panic("bookmarks service is not configured")
	}
	return s.bookmarks
}

func writeBookmarksError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, bookmarks.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, bookmarks.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, bookmarks.ErrInvalidInput), errors.Is(err, bookmarks.ErrNotAudiobook):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, bookmarks.ErrDisabled):
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

// recordAudiobookListeningSession is the playback-side hook that turns a
// progress write into a row in `listening_sessions`. We only call this for
// audiobook playback — podcast scrobbles live in last.fm + the podcast
// progress overlay, not in this audiobook-only table.
func (s *Server) recordAudiobookListeningSession(ctx context.Context, userID string, audiobookID string, before, after catalog.PlaybackState, patch *playback.PatchInput) {
	if s.bookmarks == nil {
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
	_, _ = s.bookmarks.RecordSession(ctx, userID, bookmarks.RecordSessionInput{
		AudiobookID:          audiobookID,
		StartPositionSeconds: start,
		EndPositionSeconds:   end,
		Completed:            after.Completed,
	})
}
