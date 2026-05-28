package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/bouliehaan/samo-server/internal/channels"
)

// ----- admin CRUD ------------------------------------------------------

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	items, err := s.channels.ListChannels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	id := r.PathValue("id")
	ch, err := s.channels.GetChannel(r.Context(), id)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input channels.CreateChannelInput
	if !readJSONBody(w, r, &input) {
		return
	}
	ch, err := s.channels.CreateChannel(r.Context(), input)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ch)
}

func (s *Server) updateChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input channels.UpdateChannelInput
	if !readJSONBody(w, r, &input) {
		return
	}
	ch, err := s.channels.UpdateChannel(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.channels.DeleteChannel(r.Context(), r.PathValue("id")); err != nil {
		writeChannelError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- sources ---------------------------------------------------------

func (s *Server) listChannelSources(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	items, err := s.channels.ListSources(r.Context(), r.PathValue("id"))
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) createChannelSource(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input channels.CreateSourceInput
	if !readJSONBody(w, r, &input) {
		return
	}
	src, err := s.channels.AddSource(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, src)
}

func (s *Server) updateChannelSource(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input channels.UpdateSourceInput
	if !readJSONBody(w, r, &input) {
		return
	}
	src, err := s.channels.UpdateSource(r.Context(), r.PathValue("sourceId"), input)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, src)
}

func (s *Server) deleteChannelSource(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.channels.DeleteSource(r.Context(), r.PathValue("sourceId")); err != nil {
		writeChannelError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- schedule rules --------------------------------------------------

func (s *Server) listChannelScheduleRules(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	items, err := s.channels.ListScheduleRules(r.Context(), r.PathValue("id"))
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) createChannelScheduleRule(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input channels.CreateScheduleRuleInput
	if !readJSONBody(w, r, &input) {
		return
	}
	rule, err := s.channels.AddScheduleRule(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) deleteChannelScheduleRule(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.channels.DeleteScheduleRule(r.Context(), r.PathValue("ruleId")); err != nil {
		writeChannelError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- now playing + preview ------------------------------------------

func (s *Server) channelNowPlaying(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	id := r.PathValue("id")
	np, err := s.channels.NowPlaying(r.Context(), id)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, np)
}

func (s *Server) channelPreviewNext(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	item, err := s.channels.PreviewNext(r.Context(), r.PathValue("id"))
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// ----- recent play log -------------------------------------------------

func (s *Server) channelRecentPlays(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	items, err := s.channels.RecentPlayLog(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

// ----- public-ish stream + playlist -----------------------------------

func (s *Server) channelPlaylist(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	id := r.PathValue("id")
	ch, err := s.channels.GetChannel(r.Context(), id)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	streamURL := publicURL(r, "/channels/"+url.PathEscape(ch.ID)+"/stream")
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	_, _ = fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1,%s\n%s\n", ch.Name, streamURL)
}

// channelStream pipes the per-channel ffmpeg output to the listener.
// Accepts ?stream_token=... so browser <audio> tags can authenticate
// without an Authorization header (same pattern as the music/cover
// stream routes).
func (s *Server) channelStream(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	id := r.PathValue("id")
	feed, contentType, detach, err := s.channels.Attach(r.Context(), id)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	defer detach()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case chunk, ok := <-feed:
			if !ok {
				return
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func writeChannelError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, channels.ErrNotFound):
		writeError(w, http.StatusNotFound, "channel not found")
	case errors.Is(err, channels.ErrInvalidID):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
