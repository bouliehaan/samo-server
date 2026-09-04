package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
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
	if !s.commitPlaylist(w, r, item) {
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
		// Import stays on the full reload: it can create many playlists at
		// once and resolve tracks as it goes, so there is no single row to
		// install.
		if !s.commitCatalog(w, r, scopeLibrary, actionUpdated, "", nil) {
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
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.catalog.MusicPlaylistForUser(principal.User.ID, r.PathValue("id")); err != nil {
		writeCatalogError(w, err)
		return
	}

	all := s.catalog.MusicTracksForPlaylist(r.PathValue("id"))
	items := all
	// This route accepted `limit` and `offset` and then ignored both, which is
	// worse than not supporting them: a client walking the offsets got the
	// WHOLE list back on every page. Both of ours walk it — the TypeScript core
	// through collectSamoPages and Android through fetchAllPages — so a
	// 1,095-track playlist was answered three times over, and the caller
	// concatenated the three into 3,285 rows with every track listed three
	// times. Measured through samo-proxy: 3 x 450 KB for one playlist opening.
	//
	// Paginating only when asked keeps the old contract for a caller that sends
	// neither parameter, which is what this route has always answered with and
	// what readPage's 50-item default would silently truncate.
	if pageRequested(r) {
		items = catalog.Paginate(all, page).Items
	}

	// After the slice, not before: the overlay is a per-track database read, so
	// a paged caller now pays for its page rather than for the whole playlist.
	var overlayErr error
	items, overlayErr = s.musicTracksWithUserPlayback(r.Context(), principal.User.ID, items)
	if overlayErr != nil {
		writeError(w, http.StatusInternalServerError, overlayErr.Error())
		return
	}
	// total is the length of the LIST, not of the page — it is what a client
	// plans its remaining offsets against.
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(all)})
}

// pageRequested reports whether the caller actually asked to be paginated.
//
// readPage cannot answer this: it folds "no limit given" and "limit=50" into
// the same PageRequest, and the difference matters on a route that has always
// returned everything.
func pageRequested(r *http.Request) bool {
	query := r.URL.Query()
	return strings.TrimSpace(query.Get("limit")) != "" ||
		strings.TrimSpace(query.Get("offset")) != ""
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
	// Update already returns the row it just wrote, so the projection is
	// installed from that rather than re-read — the response and what the next
	// GET serves are the same object by construction, which is the property a
	// full reload was being used to buy.
	if !s.commitPlaylist(w, r, item) {
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
	if !s.commitPlaylistRemoval(w, r, r.PathValue("id")) {
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
	case errors.Is(err, playlists.ErrSystemPlaylist):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, playlists.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, playlists.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
