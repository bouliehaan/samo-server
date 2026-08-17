package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/artwork"
	"github.com/bouliehaan/samo-server/internal/channels"
	"github.com/bouliehaan/samo-server/internal/log"
)

// ----- admin CRUD ------------------------------------------------------

// channelListEntry is a channel plus what it is airing right now.
//
// The live half is what turns a list of names into a rack of stations a
// listener can choose between, and it is the same shape an internet radio
// station carries (`nowPlaying`) so a client can render either without knowing
// which it is holding. It comes from the running streamer only — no database
// work per channel — so listing stays one query no matter how many there are.
type channelListEntry struct {
	channels.Channel
	NowPlaying    *channelNowAiring `json:"nowPlaying,omitempty"`
	ListenerCount int               `json:"listenerCount"`
	CoverID       string            `json:"coverId,omitempty"`
}

type channelNowAiring struct {
	Title     string `json:"title,omitempty"`
	Artist    string `json:"artist,omitempty"`
	Kind      string `json:"kind,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
}

// channelResponse is the one shape every channel route answers with, so the
// list, the detail and the cover upload cannot drift apart.
func (s *Server) channelResponse(ctx context.Context, ch channels.Channel) channelListEntry {
	entry := channelListEntry{Channel: ch, CoverID: s.channelCoverID(ctx, ch)}
	if item, startedAt, listeners, ok := s.channels.LiveNow(ch.ID); ok {
		entry.ListenerCount = listeners
		entry.NowPlaying = &channelNowAiring{
			Title:     item.Title,
			Artist:    item.Artist,
			Kind:      item.Kind,
			StartedAt: startedAt.UTC().Format(time.RFC3339),
		}
	} else {
		entry.ListenerCount = listeners
	}
	return entry
}

// channelCoverID answers "what artwork does this channel have", which is never
// "none".
//
// An uploaded cover wins. Failing that, the channel gets its generated tile —
// stored on demand rather than at create time, so channels that predate covers
// pick one up on the next list without a backfill. StoreGenerated derives the
// id from the key and dedupes by checksum, so this is one cheap lookup after
// the first call and the same channel always gets the same tile.
//
// A failure here is not worth failing a list over: the client falls back to
// whatever it draws for a coverless item, which is exactly where this started.
func (s *Server) channelCoverID(ctx context.Context, ch channels.Channel) string {
	if id := strings.TrimSpace(ch.CoverID); id != "" {
		return id
	}
	// s.covers directly rather than coversService(), which PANICS when covers
	// are not configured. That is a reasonable answer on an upload route, where
	// the request cannot be honoured at all; it is the wrong one here, where a
	// server with no cover store should list its channels perfectly happily and
	// just not have tiles for them.
	if s.covers == nil {
		return ""
	}
	tile := artwork.ChannelTile(ch.ID)
	if len(tile) == 0 {
		return ""
	}
	image, err := s.covers.StoreGenerated(ctx, "channel-placeholder:"+ch.ID, tile, "image/png")
	if err != nil {
		log.Warnf("channels: generated cover failed for %s: %v", ch.ID, err)
		return ""
	}
	return image.ID
}

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
	entries := make([]channelListEntry, 0, len(items))
	for _, channel := range items {
		entries = append(entries, s.channelResponse(r.Context(), channel))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": entries, "total": len(entries)})
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
	writeJSON(w, http.StatusOK, s.channelResponse(r.Context(), ch))
}

// uploadChannelCover replaces a channel's artwork with an uploaded image.
//
// Deliberately the same shape as the internet-radio station upload — same form
// field, same size ceiling, same admin gate — because it is the same gesture
// from the same web UI, and a station and a channel differing in how you give
// them a cover would be a difference with no reason behind it.
func (s *Server) uploadChannelCover(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "channel id is required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	if err := r.ParseMultipartForm(6 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("cover")
	if err != nil {
		writeError(w, http.StatusBadRequest, "cover file is required")
		return
	}
	defer file.Close()

	contentType := ""
	if header != nil {
		contentType = header.Header.Get("Content-Type")
	}
	image, err := s.coversService().StoreFromUpload(r.Context(), "channel:"+id, contentType, file)
	if err != nil {
		writeCoverUploadError(w, err)
		return
	}
	ch, err := s.channels.SetCover(r.Context(), id, image.ID)
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.channelResponse(r.Context(), ch))
}

// deleteChannelCover drops a custom cover, which puts the generated tile back
// rather than leaving the channel blank.
func (s *Server) deleteChannelCover(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeError(w, http.StatusServiceUnavailable, "channels disabled")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	ch, err := s.channels.SetCover(r.Context(), strings.TrimSpace(r.PathValue("id")), "")
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.channelResponse(r.Context(), ch))
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
