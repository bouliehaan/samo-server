package api

import (
	"net/http"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

func (s *Server) browseMusicFavorites(w http.ResponseWriter, r *http.Request) {
	s.writeMusicBrowse(w, r, catalog.MusicBrowseFavorites)
}

func (s *Server) browseMusicStarred(w http.ResponseWriter, r *http.Request) {
	s.writeMusicBrowse(w, r, catalog.MusicBrowseStarred)
}

func (s *Server) browseMusicRecentlyPlayed(w http.ResponseWriter, r *http.Request) {
	s.writeMusicBrowse(w, r, catalog.MusicBrowseRecentlyPlayed)
}

func (s *Server) browseMusicRecentlyAdded(w http.ResponseWriter, r *http.Request) {
	s.writeMusicBrowse(w, r, catalog.MusicBrowseRecentlyAdded)
}

func (s *Server) writeMusicBrowse(w http.ResponseWriter, r *http.Request, view catalog.MusicBrowseView) {
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
	states, err := s.loadMusicBrowseStates(r, principal.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.MusicBrowse(
		states.tracks,
		states.albums,
		states.artists,
		states.playlists,
		view,
		page,
	))
}

type musicBrowseStateMaps struct {
	artists   map[string]catalog.PlaybackState
	albums    map[string]catalog.PlaybackState
	tracks    map[string]catalog.PlaybackState
	playlists map[string]catalog.PlaybackState
}

func (s *Server) loadMusicBrowseStates(r *http.Request, userID string) (musicBrowseStateMaps, error) {
	if s.playback == nil {
		return musicBrowseStateMaps{}, nil
	}
	ctx := r.Context()
	trackStates, err := s.playback.ListForUser(ctx, userID, playback.TargetMusicTrack)
	if err != nil {
		return musicBrowseStateMaps{}, err
	}
	albumStates, err := s.playback.ListForUser(ctx, userID, playback.TargetMusicAlbum)
	if err != nil {
		return musicBrowseStateMaps{}, err
	}
	artistStates, err := s.playback.ListForUser(ctx, userID, playback.TargetMusicArtist)
	if err != nil {
		return musicBrowseStateMaps{}, err
	}
	playlistStates, err := s.playback.ListForUser(ctx, userID, playback.TargetMusicPlaylist)
	if err != nil {
		return musicBrowseStateMaps{}, err
	}
	return musicBrowseStateMaps{
		artists:   artistStates,
		albums:    albumStates,
		tracks:    trackStates,
		playlists: playlistStates,
	}, nil
}
