package api

import (
	"context"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/playback"
)

func (s *Server) notifyMusicTrackLastFM(
	ctx context.Context,
	userID string,
	trackID string,
	before catalog.PlaybackState,
	after catalog.PlaybackState,
	patch *playback.PatchInput,
	source string,
	resumeSeconds int,
) {
	if s.lastfm == nil || !s.lastfm.Enabled() || userID == "" {
		return
	}
	track, err := s.catalog.MusicTrack(trackID)
	if err != nil {
		return
	}
	if resumeSeconds > 0 && source == "stream" {
		s.lastfm.HandleStreamStart(ctx, userID, track, resumeSeconds)
		return
	}
	if patch != nil {
		s.lastfm.HandlePlaybackUpdate(ctx, userID, track, before, after, *patch)
		return
	}
	s.lastfm.HandlePlaybackPut(ctx, userID, track, before, after)
}

func (s *Server) postScrobbleEvent(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireLastFM(w)
	if !ok {
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input lastfm.ScrobbleEventInput
	if !readJSONBody(w, r, &input) {
		return
	}
	track, err := s.catalog.MusicTrack(input.TrackID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	response, err := service.HandleScrobbleEvent(r.Context(), principal.User.ID, track, input)
	if err != nil {
		writeLastFMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
