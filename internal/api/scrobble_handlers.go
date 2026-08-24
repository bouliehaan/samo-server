package api

import (
	"context"
	"net/http"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/listenbrainz"
	"github.com/bouliehaan/samo-server/internal/log"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/safego"
	"github.com/bouliehaan/samo-server/internal/scrobble"
)

// notifyMusicTrackScrobblers hands one playback observation to every scrobbling
// target that is live.
//
// Each target measures the play independently against the shared listen engine
// and keeps its own queue, so a user connected to only one of them is fully
// served — connecting Last.fm is not a precondition for ListenBrainz, or the
// other way round.
func (s *Server) notifyMusicTrackScrobblers(
	userID string,
	trackID string,
	before catalog.PlaybackState,
	after catalog.PlaybackState,
	patch *playback.PatchInput,
	source string,
	resumeSeconds int,
) {
	if userID == "" {
		return
	}
	lastfmLive := s.lastfm != nil && s.lastfm.Enabled()
	listenbrainzLive := s.listenbrainz != nil && s.listenbrainz.Enabled()
	if !lastfmLive && !listenbrainzLive {
		return
	}
	track, err := s.catalog.MusicTrack(trackID)
	if err != nil {
		return
	}

	log.Infof("scrobble notify: track=%q artist=%q source=%s before.progress=%d after.progress=%d resume=%d",
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
	input := scrobble.PlaybackInput{
		UserID:        userID,
		Track:         track,
		Before:        before,
		After:         after,
		Patch:         safePatch,
		Source:        source,
		ResumeSeconds: resumeSeconds,
		ObservedAt:    time.Now().UTC(),
	}

	if lastfmLive {
		safego.Go("last.fm playback handoff", func() {
			ctx, cancel := context.WithTimeout(s.baseCtx, 30*time.Second)
			defer cancel()
			s.lastfm.HandlePlayback(ctx, input)
		})
	}
	if listenbrainzLive {
		safego.Go("listenbrainz playback handoff", func() {
			ctx, cancel := context.WithTimeout(s.baseCtx, 30*time.Second)
			defer cancel()
			s.listenbrainz.HandlePlayback(ctx, input)
		})
	}
}

// scrobbleEventResponse keeps the shape clients already consume — the
// top-level flags are true when ANY target acted — and adds a per-target
// breakdown for anything that wants to know which one.
type scrobbleEventResponse struct {
	TrackID         string                    `json:"trackId"`
	Event           string                    `json:"event"`
	NowPlaying      bool                      `json:"nowPlaying,omitempty"`
	Scrobbled       bool                      `json:"scrobbled,omitempty"`
	Queued          bool                      `json:"queued,omitempty"`
	ProgressSeconds int                       `json:"progressSeconds,omitempty"`
	Targets         map[string]scrobbleTarget `json:"targets,omitempty"`
}

type scrobbleTarget struct {
	NowPlaying bool   `json:"nowPlaying,omitempty"`
	Scrobbled  bool   `json:"scrobbled,omitempty"`
	Queued     bool   `json:"queued,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (s *Server) postScrobbleEvent(w http.ResponseWriter, r *http.Request) {
	lastfmService := s.lastfmService()
	lastfmLive := lastfmService != nil && lastfmService.Enabled()
	listenbrainzService := s.listenbrainzService()
	listenbrainzLive := listenbrainzService != nil && listenbrainzService.Enabled()
	if !lastfmLive && !listenbrainzLive {
		writeError(w, http.StatusServiceUnavailable, "no scrobbling integration is configured")
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input scrobble.EventInput
	if !readJSONBody(w, r, &input) {
		return
	}
	// Reject a malformed event once, up front, rather than letting each target
	// discover it separately and disagree about the status code.
	event, err := scrobble.ParseEvent(input.Event)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	track, err := s.catalog.MusicTrack(input.TrackID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	response := scrobbleEventResponse{
		TrackID:         track.ID,
		Event:           string(event),
		ProgressSeconds: input.ProgressSeconds,
		Targets:         map[string]scrobbleTarget{},
	}

	if lastfmLive {
		target := scrobbleTarget{}
		result, err := lastfmService.HandleScrobbleEvent(r.Context(), principal.User.ID, track, lastfm.ScrobbleEventInput(input))
		if err != nil {
			target.Error = err.Error()
		} else {
			target = scrobbleTarget{NowPlaying: result.NowPlaying, Scrobbled: result.Scrobbled, Queued: result.Queued}
		}
		response.Targets["lastfm"] = target
	}
	if listenbrainzLive {
		target := scrobbleTarget{}
		result, err := listenbrainzService.HandleScrobbleEvent(r.Context(), principal.User.ID, track, listenbrainz.EventInput(input))
		if err != nil {
			target.Error = err.Error()
		} else {
			target = scrobbleTarget{NowPlaying: result.NowPlaying, Scrobbled: result.Scrobbled, Queued: result.Queued}
		}
		response.Targets["listenbrainz"] = target
	}

	for _, target := range response.Targets {
		response.NowPlaying = response.NowPlaying || target.NowPlaying
		response.Scrobbled = response.Scrobbled || target.Scrobbled
		response.Queued = response.Queued || target.Queued
	}
	writeJSON(w, http.StatusOK, response)
}
