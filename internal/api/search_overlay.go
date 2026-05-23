package api

import (
	"net/http"

	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/search"
)

func (s *Server) loadSearchOverlay(r *http.Request, userID string) (search.PlaybackOverlay, error) {
	if s.playback == nil {
		return search.PlaybackOverlay{}, nil
	}
	ctx := r.Context()
	overlay := search.PlaybackOverlay{}
	var err error
	if overlay.Tracks, err = s.playback.ListForUser(ctx, userID, playback.TargetMusicTrack); err != nil {
		return search.PlaybackOverlay{}, err
	}
	if overlay.Albums, err = s.playback.ListForUser(ctx, userID, playback.TargetMusicAlbum); err != nil {
		return search.PlaybackOverlay{}, err
	}
	if overlay.Artists, err = s.playback.ListForUser(ctx, userID, playback.TargetMusicArtist); err != nil {
		return search.PlaybackOverlay{}, err
	}
	if overlay.Playlists, err = s.playback.ListForUser(ctx, userID, playback.TargetMusicPlaylist); err != nil {
		return search.PlaybackOverlay{}, err
	}
	if overlay.Items, err = s.playback.ListForUser(ctx, userID, playback.TargetShelfItem); err != nil {
		return search.PlaybackOverlay{}, err
	}
	if overlay.Episodes, err = s.playback.ListForUser(ctx, userID, playback.TargetShelfEpisode); err != nil {
		return search.PlaybackOverlay{}, err
	}
	return overlay, nil
}

func (s *Server) searchService() *search.Service {
	if s.search == nil {
		panic("search service is not configured")
	}
	return s.search
}
