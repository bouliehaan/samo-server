// Package events is the server's fan-out for live UI updates.
//
// It carries state *snapshots*, never deltas. That single decision is what
// makes the rest of it simple: a subscriber that misses an event has not lost
// information, because the next one carries the whole current truth. So the
// hub can drop rather than block when a subscriber is slow, and a browser that
// tabs out or a laptop that sleeps costs the scanner nothing.
package events

import (
	"sync"
)

// Event types. These are the wire contract with the dashboard.
const (
	// TypeScanJob carries a scan job snapshot as the scan API returns it.
	TypeScanJob = "scan-job"
	// TypeArtistImages carries an artist-image backfill job snapshot.
	TypeArtistImages = "artist-images"
)

// Event is one snapshot, addressed by type.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// subscriberBuffer is how many events may queue for one subscriber before the
// hub starts dropping. Progress events supersede each other, so a handful of
// slots is plenty: the depth only needs to absorb a burst, not a backlog.
const subscriberBuffer = 8

// Hub fans events out to every current subscriber.
//
// A nil *Hub is a working no-op. Services take one optionally, and a nil check
// at every publish site is noise that eventually gets forgotten at one of them.
type Hub struct {
	mu     sync.Mutex
	subs   map[int]chan Event
	nextID int
}

// NewHub returns a hub with no subscribers.
func NewHub() *Hub {
	return &Hub{subs: make(map[int]chan Event)}
}

// Subscribe returns a channel of events and a function that stops the
// subscription. The channel is closed by cancel, so a range over it ends
// cleanly. cancel is idempotent.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	if h == nil {
		// A closed channel so callers can select on it forever without a
		// special case; nothing will ever be sent.
		dead := make(chan Event)
		close(dead)
		return dead, func() {}
	}

	ch := make(chan Event, subscriberBuffer)
	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.subs[id] = ch
	h.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			// Both the delete and the close happen under the lock Publish
			// holds while sending, so a send can never race the close.
			h.mu.Lock()
			delete(h.subs, id)
			close(ch)
			h.mu.Unlock()
		})
	}
}

// Publish delivers an event to every subscriber that can take it right now.
//
// It never blocks and never fails. A subscriber whose buffer is full misses
// this event and gets the next one — which, because these are snapshots,
// carries everything the dropped one would have.
func (h *Hub) Publish(event Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- event:
		default:
		}
	}
}

// Subscribers reports how many subscriptions are open. Used by the health
// endpoint and by tests; not part of the event path.
func (h *Hub) Subscribers() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
