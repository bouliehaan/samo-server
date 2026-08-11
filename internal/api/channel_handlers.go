package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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

// channelScheduleStatus answers "why is my show not playing".
func (s *Server) channelScheduleStatus(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	status, err := s.channels.ScheduleStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// ----- the programming plan --------------------------------------------

// readRawBody reads a request body whole.
//
// The plan endpoint needs the bytes rather than a decoded struct, because
// channels.ParsePlan does its own strict decode — unknown fields rejected — and
// reports every problem in the document at once. Decoding twice would mean the
// API's error messages and the engine's disagreed about what a valid plan is.
func readRawBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return nil, false
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty body")
		return nil, false
	}
	return body, true
}

// channelPlan returns the station's plan — the stored one, or the plan its
// existing sources and booked slots already describe.
//
// Always returns something valid, because "edit the plan you are already
// running" is the only sane way in: an empty text box would make the first edit
// a rewrite of the whole station.
func (s *Server) channelPlan(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	view, err := s.channels.GetPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// putChannelPlan validates and stores a plan.
//
// Validation is the whole value of this endpoint: a plan that names a pool that
// does not exist, or whose blocks hand over to each other in a loop, or that
// has no default block to fall back to, would take the station off the air at
// some unpredictable hour. It is rejected here with every problem listed at
// once rather than the first one found.
func (s *Server) putChannelPlan(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	body, ok := readRawBody(w, r)
	if !ok {
		return
	}
	view, err := s.channels.SetPlan(r.Context(), r.PathValue("id"), body)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// deleteChannelPlan drops a custom plan and returns the channel to the derived
// one, which is the escape hatch from an edit that made things worse.
func (s *Server) deleteChannelPlan(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.channels.ResetPlan(r.Context(), r.PathValue("id")); err != nil {
		writeChannelError(w, err)
		return
	}
	view, err := s.channels.GetPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// channelObligations is what the station owes the listener.
//
// The question it answers is "my new episode has not played, is the station
// even aware of it" — which used to be unanswerable, because there was nothing
// to be aware with.
func (s *Server) channelObligations(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	owed, err := s.channels.Owed(r.Context(), r.PathValue("id"))
	if err != nil {
		writeChannelError(w, err)
		return
	}
	pending := 0
	for _, obligation := range owed {
		if obligation.State == channels.ObligationPending {
			pending++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": owed, "pending": pending, "total": len(owed)})
}

// channelWhy answers "why the hell did it play that".
//
// Returns the recorded decisions for a channel that has been on air, and — for
// one that has not — what it would decide right now, which is the only way to
// debug a station that is silent.
func (s *Server) channelWhy(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	limit := 1
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	decisions, err := s.channels.Why(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": decisions, "total": len(decisions)})
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

// skipChannel moves a running channel on: past the item, or past where the
// item came from.
func (s *Server) skipChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	scope := channels.SkipItem
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope"))) {
	case string(channels.SkipKind), string(channels.SkipSource):
		scope = channels.SkipKind
	}
	skipped, err := s.channels.Skip(r.Context(), r.PathValue("id"), scope)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	if !skipped {
		// Nothing was playing. Not an error — the channel simply has no
		// listeners, and the ladder picks fresh when somebody tunes in.
		writeJSON(w, http.StatusOK, map[string]any{"scope": string(scope), "skipped": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scope": string(scope), "skipped": true})
}

// previousChannel re-airs the last item, since a live stream has nothing to
// rewind into.
func (s *Server) previousChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	moved, err := s.channels.Previous(r.Context(), r.PathValue("id"))
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moved": moved})
}

func (s *Server) clearChannelSkips(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusNotFound, "channels disabled")
		return
	}
	if err := s.channels.ClearSkips(r.Context(), r.PathValue("id")); err != nil {
		writeChannelError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

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
