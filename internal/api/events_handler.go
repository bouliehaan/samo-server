package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bouliehaan/samo-server/internal/events"
	"github.com/bouliehaan/samo-server/internal/log"
)

// heartbeatInterval keeps an idle stream alive through proxies that reap quiet
// connections. Samo is commonly reached through a Cloudflare tunnel, which
// times an idle connection out at 100s, so this has to be comfortably under
// that. The payload is an SSE comment, which clients ignore by definition.
const heartbeatInterval = 25 * time.Second

// eventStream serves the dashboard's live update channel.
//
// The client reads this with fetch() rather than EventSource so the bearer
// token rides in a header. EventSource cannot set headers, which would have
// meant putting a stream token in the query string — the codebase already
// treats URL-borne credentials as a leak vector (Referer, access logs), and a
// 30-minute token would additionally break the stream on expiry.
//
// Every event carries a full snapshot, so a client that reconnects after a
// dropped connection needs no replay: the next event tells it everything. That
// is why there is no Last-Event-ID handling here.
func (s *Server) eventStream(w http.ResponseWriter, r *http.Request) {
	// Scan and backfill progress is admin-only, same as the endpoints that
	// used to be polled for it.
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	// Tell nginx-style reverse proxies not to buffer; without it the events
	// arrive in batches whenever the proxy's buffer happens to fill.
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	stream, cancel := s.events.Subscribe()
	defer cancel()

	// An immediate comment settles the connection before anything is
	// published, so the client's "connected" state does not wait on the first
	// scan to start.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case event, open := <-stream:
			if !open {
				return
			}
			if err := writeSSEEvent(w, event); err != nil {
				// A write error is a client that went away mid-send; the
				// context usually closes a moment later anyway.
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSEEvent frames one event.
//
// The data is a single line of JSON. json.Marshal never emits a raw newline,
// so no multi-line data folding is needed and the client's parser stays a
// split on blank lines.
func writeSSEEvent(w http.ResponseWriter, event events.Event) error {
	payload, err := json.Marshal(event.Data)
	if err != nil {
		// Dropping one malformed snapshot beats killing the stream: the next
		// one supersedes it anyway.
		log.Warnf("events: marshal %s payload: %v", event.Type, err)
		return nil
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, payload)
	return err
}
