package api

import (
	"net/http"
	"sort"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/search"
)

func (s *Server) listMusicArtists(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	options := readMusicListOptions(r, page)
	if !s.applyMusicListPlaybackToOptions(w, r, &options) {
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListMusicArtistsSorted(options))
}

func (s *Server) getMusicArtist(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.MusicArtist(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	// Merge cached bio + similar artists (and self-heal on a miss). Non-blocking:
	// fresh lookups land via a background resolve, surfaced on the next read/sync.
	if s.artistMeta != nil {
		s.artistMeta.Hydrate(r.Context(), &item)
	}
	if principal, ok := s.currentUser(r); ok {
		item, err = s.musicArtistWithUserPlayback(r.Context(), principal.User.ID, item)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, item)
}

// listMusicArtistAlbums returns the album list for an artist so the dashboard
// can render an artist detail page without scanning the whole catalog client
// side.
func (s *Server) listMusicArtistAlbums(w http.ResponseWriter, r *http.Request) {
	artistID := r.PathValue("id")
	if _, err := s.catalog.MusicArtist(artistID); err != nil {
		writeCatalogError(w, err)
		return
	}
	albums := s.catalog.MusicAlbumsForArtist(artistID)
	if principal, ok := s.currentUser(r); ok {
		var err error
		albums, err = s.musicAlbumsWithUserPlayback(r.Context(), principal.User.ID, artistID, albums)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": albums, "total": len(albums)})
}

// listMusicArtistTopTracks returns the artist's most-played tracks for the
// "Top Tracks" rail. Ranking is by THIS user's play count, so the overlay must
// run before the sort; without a user it falls back to the catalog's order.
func (s *Server) listMusicArtistTopTracks(w http.ResponseWriter, r *http.Request) {
	artistID := r.PathValue("id")
	if _, err := s.catalog.MusicArtist(artistID); err != nil {
		writeCatalogError(w, err)
		return
	}
	tracks := s.catalog.MusicTracksForArtist(artistID)
	if principal, ok := s.currentUser(r); ok {
		var err error
		tracks, err = s.musicTracksWithUserPlayback(r.Context(), principal.User.ID, tracks)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	sort.SliceStable(tracks, func(i, j int) bool {
		return tracks[i].Playback.PlayCount > tracks[j].Playback.PlayCount
	})
	limit := readLimitParam(r, 5, 50)
	if len(tracks) > limit {
		tracks = tracks[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": tracks, "total": len(tracks)})
}

// listMusicArtistAppearsOn returns albums the artist features on without being
// the album artist (see catalog.MusicArtistAppearsOnAlbums).
func (s *Server) listMusicArtistAppearsOn(w http.ResponseWriter, r *http.Request) {
	artistID := r.PathValue("id")
	if _, err := s.catalog.MusicArtist(artistID); err != nil {
		writeCatalogError(w, err)
		return
	}
	albums := s.catalog.MusicArtistAppearsOnAlbums(artistID)
	limit := readLimitParam(r, 20, 100)
	if len(albums) > limit {
		albums = albums[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": albums, "total": len(albums)})
}

func (s *Server) listMusicAlbums(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	options := readMusicListOptions(r, page)
	if !s.applyMusicListPlaybackToOptions(w, r, &options) {
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListMusicAlbumsSorted(options))
}

func (s *Server) getMusicAlbum(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.MusicAlbum(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	if principal, ok := s.currentUser(r); ok {
		item, err = s.musicAlbumWithUserPlayback(r.Context(), principal.User.ID, item)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listMusicAlbumTracks(w http.ResponseWriter, r *http.Request) {
	albumID := r.PathValue("id")
	if _, err := s.catalog.MusicAlbum(albumID); err != nil {
		writeCatalogError(w, err)
		return
	}
	items := s.catalog.MusicTracksForAlbum(albumID)
	if principal, ok := s.currentUser(r); ok {
		var err error
		items, err = s.musicTracksWithUserPlayback(r.Context(), principal.User.ID, items)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) listMusicTracks(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	options := readMusicListOptions(r, page)
	if !s.applyMusicListPlaybackToOptions(w, r, &options) {
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListMusicTracksSorted(options))
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
	result := s.catalog.ListMusicPlaylistsForUser(principal.User.ID, page)
	if s.playback != nil && len(result.Items) > 0 {
		ids := make([]string, 0, len(result.Items))
		for _, item := range result.Items {
			ids = append(ids, item.ID)
		}
		states, err := s.playback.ListForUserByIDs(
			r.Context(),
			principal.User.ID,
			playback.TargetMusicPlaylist,
			ids,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for index := range result.Items {
			if state, ok := states[result.Items[index].ID]; ok {
				result.Items[index].Playback = state
			}
		}
	}
	writeJSON(w, http.StatusOK, result)
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
	item, err = s.musicPlaylistWithUserPlayback(r.Context(), principal.User.ID, item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
