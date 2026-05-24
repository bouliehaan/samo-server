package subsonic

import (
	"net/http"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

func (s *Server) getStarred(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeFailed(w, 40, "Wrong username or password")
		return
	}
	states, err := s.loadMusicPlayback(r.Context(), principal.User.ID)
	if err != nil {
		s.writeFailed(w, 0, err.Error())
		return
	}

	artists := make([]artist, 0)
	for _, item := range s.catalog.ListMusicArtists(catalog.PageRequest{Limit: 5000}).Items {
		item = overlayArtist(item, states)
		if item.Playback.Starred {
			artists = append(artists, toArtist(item))
		}
	}

	albums := make([]child, 0)
	for _, item := range s.catalog.ListMusicAlbums(catalog.PageRequest{Limit: 5000}).Items {
		item = overlayAlbum(item, states)
		if item.Playback.Starred {
			albums = append(albums, applyPlaybackChild(toAlbumChild(item), item.Playback))
		}
	}

	songs := make([]child, 0)
	for _, item := range s.catalog.ListMusicTracks(catalog.PageRequest{Limit: 5000}).Items {
		item = overlayTrack(item, states)
		if item.Playback.Starred {
			songs = append(songs, applyPlaybackChild(toSongChild(item), item.Playback))
		}
	}

	s.writeOK(w, responseBody{Starred: &starredResult{
		Artist: artists,
		Album:  albums,
		Song:   songs,
	}})
}

func (s *Server) star(w http.ResponseWriter, r *http.Request) {
	s.setStarred(w, r, true)
}

func (s *Server) unstar(w http.ResponseWriter, r *http.Request) {
	s.setStarred(w, r, false)
}

func (s *Server) setStarred(w http.ResponseWriter, r *http.Request, starred bool) {
	if s.playback == nil {
		s.writeFailed(w, 0, "playback is not configured")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		s.writeFailed(w, 10, "required parameter id is missing")
		return
	}
	kind, err := s.resolveMusicTargetKind(id)
	if err != nil {
		s.writeFailed(w, 70, "item not found")
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeFailed(w, 40, "Wrong username or password")
		return
	}
	if _, err := s.playback.Patch(r.Context(), principal.User.ID, kind, id, playback.PatchInput{
		Starred: &starred,
	}); err != nil {
		s.writeFailed(w, 0, err.Error())
		return
	}
	s.writeOK(w, responseBody{})
}

func (s *Server) setRating(w http.ResponseWriter, r *http.Request) {
	if s.playback == nil {
		s.writeFailed(w, 0, "playback is not configured")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		s.writeFailed(w, 10, "required parameter id is missing")
		return
	}
	rating := intQuery(r, "rating", -1)
	if rating < 0 || rating > 5 {
		s.writeFailed(w, 10, "rating must be between 0 and 5")
		return
	}
	kind, err := s.resolveMusicTargetKind(id)
	if err != nil {
		s.writeFailed(w, 70, "item not found")
		return
	}
	principal, ok := principalFromContext(r.Context())
	if !ok {
		s.writeFailed(w, 40, "Wrong username or password")
		return
	}
	if _, err := s.playback.Patch(r.Context(), principal.User.ID, kind, id, playback.PatchInput{
		Rating: &rating,
	}); err != nil {
		s.writeFailed(w, 0, err.Error())
		return
	}
	s.writeOK(w, responseBody{})
}

func (s *Server) resolveMusicTargetKind(id string) (playback.TargetKind, error) {
	if _, err := s.catalog.MusicTrack(id); err == nil {
		return playback.TargetMusicTrack, nil
	}
	if _, err := s.catalog.MusicAlbum(id); err == nil {
		return playback.TargetMusicAlbum, nil
	}
	if _, err := s.catalog.MusicArtist(id); err == nil {
		return playback.TargetMusicArtist, nil
	}
	return "", catalog.ErrNotFound
}
