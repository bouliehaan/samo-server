package api

import (
	"context"
	"net/http"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/log"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/safego"
)

func (s *Server) notifyMusicTrackLastFM(
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

	log.Infof("last.fm notify: track=%q artist=%q source=%s before.progress=%d after.progress=%d resume=%d",
		track.Title, track.DisplayArtist, source, before.ProgressSeconds, after.ProgressSeconds, resumeSeconds)

	var safePatch *playback.PatchInput
	if patch != nil {
		p := *patch
		safePatch = &p
	}

	// Stamp the observation with the moment the request arrived, not the moment
	// the worker gets to it. Each notification runs on its own goroutine, so
	// without this a progress report that overtakes an earlier one would look
	// like the listener had seeked backwards.
	input := lastfm.PlaybackInput{
		UserID:        userID,
		Track:         track,
		Before:        before,
		After:         after,
		Patch:         safePatch,
		Source:        source,
		ResumeSeconds: resumeSeconds,
		ObservedAt:    time.Now().UTC(),
	}

	safego.Go("last.fm playback handoff", func() {
		ctx, cancel := context.WithTimeout(s.baseCtx, 30*time.Second)
		defer cancel()
		s.lastfm.HandlePlayback(ctx, input)
	})
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
