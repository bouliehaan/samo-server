package api

import (
	"errors"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

func (s *Server) getPlayback(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	kind, err := playback.ParseTargetKind(r.PathValue("kind"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	state, err := s.playbackService().Get(r.Context(), principal.User.ID, kind, r.PathValue("id"))
	if err != nil {
		writePlaybackError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) putPlayback(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	kind, err := playback.ParseTargetKind(r.PathValue("kind"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	var before catalog.PlaybackState
	if kind == playback.TargetMusicTrack || kind == playback.TargetAudiobook {
		if state, err := s.playbackService().Get(r.Context(), principal.User.ID, kind, id); err == nil {
			before = state
		}
	}
	var state catalog.PlaybackState
	if !readJSONBody(w, r, &state) {
		return
	}
	updated, err := s.playbackService().Put(r.Context(), principal.User.ID, kind, id, state)
	if err != nil {
		writePlaybackError(w, err)
		return
	}
	if kind == playback.TargetMusicTrack {
		rollupPatch := playback.PatchInput{
			IncrementPlayCount: updated.PlayCount > before.PlayCount,
			TouchLastPlayedAt:  playbackLastPlayedAdvanced(before, updated),
		}
		s.rollupMusicTrackPlayback(r.Context(), principal.User.ID, id, rollupPatch)
		s.notifyMusicTrackScrobblers(principal.User.ID, id, before, updated, nil, "playback-put", 0)
	}
	if kind == playback.TargetAudiobook {
		s.recordAudiobookListeningSession(r.Context(), principal.User.ID, id, before, updated, nil)
	}
	writeJSON(w, http.StatusOK, updated)
}

func playbackLastPlayedAdvanced(before, after catalog.PlaybackState) bool {
	if after.LastPlayedAt == nil {
		return false
	}
	if before.LastPlayedAt == nil {
		return true
	}
	return after.LastPlayedAt.After(*before.LastPlayedAt)
}

func (s *Server) patchPlayback(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	kind, err := playback.ParseTargetKind(r.PathValue("kind"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	var before catalog.PlaybackState
	if (kind == playback.TargetMusicTrack && s.lastfm != nil && s.lastfm.Enabled()) || kind == playback.TargetAudiobook {
		if state, err := s.playbackService().Get(r.Context(), principal.User.ID, kind, id); err == nil {
			before = state
		}
	}
	var patch playback.PatchInput
	if !readJSONBody(w, r, &patch) {
		return
	}
	if kind == playback.TargetMusicTrack {
		s.dropImpossibleMusicPosition(id, &patch)
	}
	updated, err := s.playbackService().Patch(r.Context(), principal.User.ID, kind, id, patch)
	if err != nil {
		writePlaybackError(w, err)
		return
	}
	if kind == playback.TargetMusicTrack {
		s.rollupMusicTrackPlayback(r.Context(), principal.User.ID, id, patch)
		s.notifyMusicTrackScrobblers(principal.User.ID, id, before, updated, &patch, "playback-patch", 0)
	}
	if kind == playback.TargetAudiobook {
		s.recordAudiobookListeningSession(r.Context(), principal.User.ID, id, before, updated, &patch)
	}
	writeJSON(w, http.StatusOK, updated)
}

// dropImpossibleMusicPosition discards a reported position past the end of the
// track. The desktop/web renderer sent Jellyfin ticks (seconds * 1e7) as seconds
// on every pause and seek, so a track 3.7s in was stored at 37,550,000. A song
// never resumes from its position, but scrobbling and listening inference read
// it, and a number that cannot be true is worse than none.
func (s *Server) dropImpossibleMusicPosition(trackID string, patch *playback.PatchInput) {
	if patch.ProgressSeconds == nil || s.catalog == nil {
		return
	}
	track, err := s.catalog.MusicTrack(trackID)
	if err != nil || track.DurationSeconds <= 0 {
		return
	}
	if *patch.ProgressSeconds > track.DurationSeconds+5 {
		patch.ProgressSeconds = nil
	}
}

func (s *Server) playbackService() *playback.Service {
	if s.playback == nil {
		panic("playback service is not configured")
	}
	return s.playback
}

func writePlaybackError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, playback.ErrNotFound):
		writeError(w, http.StatusNotFound, "playback target not found")
	case errors.Is(err, playback.ErrInvalidTarget):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, playback.ErrInvalidState):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
