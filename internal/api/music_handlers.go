package api

import (
	"net/http"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/search"
)

func (s *Server) listMusicArtists(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListMusicArtistsSorted(readMusicListOptions(r, page)))
}

func (s *Server) getMusicArtist(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.MusicArtist(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// listMusicArtistAlbums returns the album list for an artist so the dashboard
// can render an artist detail page without scanning the whole catalog client
// side.
func (s *Server) listMusicArtistAlbums(w http.ResponseWriter, r *http.Request) {
	if _, err := s.catalog.MusicArtist(r.PathValue("id")); err != nil {
		writeCatalogError(w, err)
		return
	}
	albums := s.catalog.MusicAlbumsForArtist(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"items": albums, "total": len(albums)})
}

func (s *Server) listMusicAlbums(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListMusicAlbumsSorted(readMusicListOptions(r, page)))
}

func (s *Server) getMusicAlbum(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.MusicAlbum(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listMusicTracks(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListMusicTracksSorted(readMusicListOptions(r, page)))
}

func (s *Server) getMusicTrack(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	item, err := s.musicTrackWithUserPlayback(r.Context(), principal.User.ID, r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listMusicGenres(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListGenres(page))
}

func (s *Server) listMusicPlaylists(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, s.catalog.ListMusicPlaylistsForUser(principal.User.ID, page))
}

func (s *Server) getMusicPlaylist(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	item, err := s.catalog.MusicPlaylistForUser(principal.User.ID, r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) searchMusic(w http.ResponseWriter, r *http.Request) {
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
	overlay, err := s.loadSearchOverlay(r, principal.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	query := search.ParseMusicQueryFromRequest(r, page)
	query.PlaylistUserID = principal.User.ID
	query.FilterPlaylistsByUser = true
	writeJSON(w, http.StatusOK, s.searchService().SearchMusic(query, overlay))
}

func readMusicListOptions(r *http.Request, page catalog.PageRequest) catalog.MusicListOptions {
	direction := r.URL.Query().Get("direction")
	if direction == "" {
		direction = r.URL.Query().Get("order")
	}
	return catalog.MusicListOptions{
		Page:      page,
		Sort:      r.URL.Query().Get("sort"),
		Direction: direction,
	}
}
