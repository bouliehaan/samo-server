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
	// Catalog-change notifications are deliberately withheld here rather than
	// added to this stream: this is the dashboard's channel, its consumer
	// switches on a closed set of job types, and widening the contract of a
	// working stream to save a handler is how a dashboard starts logging
	// unknown events. They have their own endpoint.
	s.streamEvents(w, r, func(event events.Event) bool {
		return event.Type != events.TypeCatalogChanged
	})
}

// catalogEventStream is the live "something you are looking at has changed"
// channel, for every signed-in client rather than only admins.
//
// It exists because both clients were otherwise guessing at freshness: the
// desktop refetched on a timer and on mount, and the phone waited up to half
// an hour for its next background sync, so a playlist edited on one was
// invisible on the other until something happened to ask. The payload carries
// no catalog content — only which scope changed — so it needs none of the
// admin gating the dashboard stream has.
func (s *Server) catalogEventStream(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.streamEvents(w, r, func(event events.Event) bool {
		return event.Type == events.TypeCatalogChanged
	})
}

// streamEvents is the shared SSE loop: headers, an immediate settling comment,
// heartbeats, and forwarding of whatever `include` admits.
func (s *Server) streamEvents(
	w http.ResponseWriter,
	r *http.Request,
	include func(events.Event) bool,
) {
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
			if include != nil && !include(event) {
				continue
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
