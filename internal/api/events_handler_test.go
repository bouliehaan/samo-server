package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/events"
	"github.com/bouliehaan/samo-server/internal/users"
)

// adminRequest builds a request already carrying an admin principal, the way
// the requireUser middleware would have left it.
func adminRequest(server *Server, ctx context.Context) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	principal := users.Principal{User: users.User{ID: "user-1", Role: users.RoleAdmin}}
	return req.WithContext(server.withPrincipal(req.Context(), principal))
}

func TestEventStreamDeliversPublishedEvents(t *testing.T) {
	hub := events.NewHub()
	server := &Server{events: hub, mux: http.NewServeMux()}

	rec := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := adminRequest(server, ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.eventStream(rec, req)
	}()

	// Give the handler a moment to subscribe, then publish.
	deadline := time.Now().Add(2 * time.Second)
	for hub.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if hub.Subscribers() != 1 {
		t.Fatalf("handler did not subscribe (subscribers=%d)", hub.Subscribers())
	}
	hub.Publish(events.Event{Type: events.TypeScanJob, Data: map[string]string{"id": "scan_1"}})

	// Let the write land, then stop the handler and inspect what it wrote.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q; proxies will batch the stream", got)
	}

	body := rec.Body.String()
	if !strings.Contains(body, ": connected") {
		t.Error("no opening comment; clients cannot tell the stream is live until the first event")
	}
	if !strings.Contains(body, "event: "+events.TypeScanJob) {
		t.Errorf("missing event type in:\n%s", body)
	}
	if !strings.Contains(body, `data: {"id":"scan_1"}`) {
		t.Errorf("missing or misframed payload in:\n%s", body)
	}
}

// The handler must let go of its subscription when the client disconnects,
// or a browser that opens and closes the dashboard leaks a subscriber per
// visit and the hub fans out to an ever-growing set of dead channels.
func TestEventStreamUnsubscribesOnDisconnect(t *testing.T) {
	hub := events.NewHub()
	server := &Server{events: hub, mux: http.NewServeMux()}

	ctx, cancel := context.WithCancel(context.Background())
	req := adminRequest(server, ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.eventStream(httptest.NewRecorder(), req)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for hub.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if hub.Subscribers() != 0 {
		t.Fatalf("subscribers = %d after disconnect, want 0", hub.Subscribers())
	}
}

func TestEventStreamRequiresAdmin(t *testing.T) {
	server := &Server{events: events.NewHub(), mux: http.NewServeMux()}
	rec := httptest.NewRecorder()
	server.eventStream(rec, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
