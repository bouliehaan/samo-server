package api

import (
	"encoding/json"
	"fmt"
	"github.com/bouliehaan/samo-server/internal/users"
	"net/http"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/heroes"
	"github.com/bouliehaan/samo-server/internal/playback"
)

// getHomeHeroes answers "what should I play right now" for the caller: the
// ranked cards both clients lead Home with. See package heroes for what earns
// a card. The handler only gathers what the ranking looks at — the user's
// playlists and their tracks, every episode and the user's state for each —
// and hands over the clock.
func (s *Server) getHomeHeroes(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	userID := principal.User.ID
	ctx := r.Context()

	in := heroes.Input{
		Playlists: collectPages(func(page catalog.PageRequest) catalog.Page[catalog.MusicPlaylist] {
			return s.catalog.ListMusicPlaylistsForUser(userID, page)
		}),
		PlaylistTracks: s.catalog.MusicTracksForPlaylist,
		Podcasts:       collectPages(s.catalog.ListPodcasts),
		Episodes:       collectPages(s.catalog.ListPodcastEpisodes),
		Albums:         collectPages(s.catalog.ListMusicAlbums),
		Books:          collectPages(s.catalog.ListAudiobooks),
	}
	if s.playback != nil {
		var err error
		if in.PlaylistStates, err = s.playback.ListForUser(ctx, userID, playback.TargetMusicPlaylist); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if in.EpisodeStates, err = s.playback.ListForUser(ctx, userID, playback.TargetPodcastEpisode); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, request := range []struct {
			kind playback.TargetKind
			out  *map[string]catalog.PlaybackState
		}{
			{playback.TargetMusicAlbum, &in.AlbumStates},
			{playback.TargetAudiobook, &in.BookStates},
			{playback.TargetPodcast, &in.ShowStates},
		} {
			if *request.out, err = s.playback.ListForUser(ctx, userID, request.kind); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if in.RadioEpisodeStates, err = s.playback.ListForUser(ctx, users.BootstrapUserID, playback.TargetPodcastEpisode); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if in.TrackStates, err = s.playback.ListForUser(ctx, userID, playback.TargetMusicTrack); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		catalog.OverlayMusicAlbums(in.Albums, in.AlbumStates, collectPages(s.catalog.ListMusicTracks), in.TrackStates)
		if in.AlbumStates == nil {
			in.AlbumStates = map[string]catalog.PlaybackState{}
		}
		for _, album := range in.Albums {
			in.AlbumStates[album.ID] = album.Playback
		}

	}

	// Client visit IDs give repeatable picks within a visit. Older clients
	// still get variety across six-hour windows, scoped to the listener.
	session := r.URL.Query().Get("session")
	if len(session) > 128 {
		writeError(w, http.StatusBadRequest, "session is too long")
		return
	}
	if session == "" {
		session = fmt.Sprint(time.Now().Unix() / (6 * 60 * 60))
	}
	in.SessionKey = userID + ":" + session
	if seen := r.URL.Query().Get("seen"); seen != "" {
		if len(seen) > 2048 || json.Unmarshal([]byte(seen), &in.RecentlyShown) != nil || len(in.RecentlyShown) > 8 {
			writeError(w, http.StatusBadRequest, "invalid seen targets")
			return
		}
	}

	items := heroes.Rank(in, time.Now())
	if items == nil {
		items = []heroes.Hero{}
	}
	writeJSON(w, http.StatusOK, struct {
		Items []heroes.Hero `json:"items"`
	}{Items: items})
}

// collectPages walks a paged catalog listing to the end. The catalog caps a
// page at 500; the ranking needs every episode to know which shows the
// listener finishes, not the first 500.
func collectPages[T any](list func(catalog.PageRequest) catalog.Page[T]) []T {
	const pageSize = 500
	var items []T
	for offset := 0; ; offset += pageSize {
		page := list(catalog.PageRequest{Limit: pageSize, Offset: offset})
		items = append(items, page.Items...)
		if len(page.Items) < pageSize || offset+len(page.Items) >= page.Total {
			return items
		}
	}
}
