package subsonic

import (
	"math/rand"
	"net/http"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func (s *Server) getRandomSongs(w http.ResponseWriter, r *http.Request) {
	page := s.catalog.ListMusicTracks(catalog.PageRequest{Limit: 5000})
	tracks := append([]catalog.MusicTrack(nil), page.Items...)
	if len(tracks) == 0 {
		s.writeOK(w, responseBody{RandomSongs: &randomSongs{Song: nil}})
		return
	}

	principal, ok := principalFromContext(r.Context())
	if ok && s.playback != nil {
		states, err := s.loadMusicPlayback(r.Context(), principal.User.ID)
		if err == nil {
			for i := range tracks {
				tracks[i] = overlayTrack(tracks[i], states)
			}
		}
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	rng.Shuffle(len(tracks), func(i, j int) {
		tracks[i], tracks[j] = tracks[j], tracks[i]
	})

	size := intQuery(r, "size", 10)
	if size < 1 {
		size = 10
	}
	if size > len(tracks) {
		size = len(tracks)
	}
	tracks = tracks[:size]

	songs := make([]child, 0, len(tracks))
	for _, track := range tracks {
		songs = append(songs, applyPlaybackChild(toSongChild(track), track.Playback))
	}
	s.writeOK(w, responseBody{RandomSongs: &randomSongs{Song: songs}})
}

func (s *Server) albumListFromBrowse(w http.ResponseWriter, r *http.Request, view catalog.MusicBrowseView) {
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
	page := catalog.PageRequest{Limit: intQuery(r, "size", 10), Offset: intQuery(r, "offset", 0)}
	if page.Limit <= 0 {
		page.Limit = 10
	}
	if page.Limit > 500 {
		page.Limit = 500
	}
	results := s.catalog.MusicBrowseForUser(states.tracks, states.albums, states.artists, states.playlists, view, page, principal.User.ID)
	children := make([]child, 0, len(results.Albums))
	for _, album := range results.Albums {
		children = append(children, applyPlaybackChild(toAlbumChild(album), album.Playback))
	}
	s.writeOK(w, responseBody{AlbumList2: &albumList2{Album: children}})
}
