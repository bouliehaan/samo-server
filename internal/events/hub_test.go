package events

import (
	"sync"
	"testing"
)

func TestPublishReachesEverySubscriber(t *testing.T) {
	hub := NewHub()
	a, cancelA := hub.Subscribe()
	b, cancelB := hub.Subscribe()
	defer cancelA()
	defer cancelB()

	hub.Publish(Event{Type: TypeScanJob, Data: "one"})

	for name, ch := range map[string]<-chan Event{"a": a, "b": b} {
		select {
		case got := <-ch:
			if got.Data != "one" {
				t.Errorf("%s: got %v", name, got.Data)
			}
		default:
			t.Errorf("%s: received nothing", name)
		}
	}
}

// The property the whole design rests on: a subscriber that stops reading must
// not be able to block a publisher. If this ever regresses, a browser tab that
// sleeps mid-scan wedges the scanner.
func TestPublishNeverBlocksOnAFullSubscriber(t *testing.T) {
	hub := NewHub()
	_, cancel := hub.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBuffer*100; i++ {
			hub.Publish(Event{Type: TypeScanJob, Data: i})
		}
		close(done)
	}()
	<-done // A blocking Publish would hang here and the test would time out.
}

// Dropping is only acceptable because events are snapshots — so what a slow
// subscriber must still get is the *latest* state once it drains.
func TestSlowSubscriberStillSeesLaterEvents(t *testing.T) {
	hub := NewHub()
	ch, cancel := hub.Subscribe()
	defer cancel()

	for i := 0; i < subscriberBuffer*4; i++ {
		hub.Publish(Event{Type: TypeScanJob, Data: i})
	}
	// Drain what buffered, then confirm a fresh publish still arrives.
	for len(ch) > 0 {
		<-ch
	}
	hub.Publish(Event{Type: TypeScanJob, Data: "latest"})
	select {
	case got := <-ch:
		if got.Data != "latest" {
			t.Fatalf("got %v, want latest", got.Data)
		}
	default:
		t.Fatal("subscriber received nothing after draining")
	}
}

func TestCancelUnsubscribesAndClosesTheChannel(t *testing.T) {
	hub := NewHub()
	ch, cancel := hub.Subscribe()
	if hub.Subscribers() != 1 {
		t.Fatalf("subscribers = %d, want 1", hub.Subscribers())
	}

	cancel()
	cancel() // Idempotent: a double close would panic.

	if hub.Subscribers() != 0 {
		t.Fatalf("subscribers = %d after cancel, want 0", hub.Subscribers())
	}
	if _, open := <-ch; open {
		t.Fatal("channel still open after cancel")
	}
	hub.Publish(Event{Type: TypeScanJob}) // Must not panic sending to a closed sub.
}

// A nil hub is a working no-op so services can hold one unconditionally.
func TestNilHubIsUsable(t *testing.T) {
	var hub *Hub
	hub.Publish(Event{Type: TypeScanJob})
	if hub.Subscribers() != 0 {
		t.Fatal("nil hub reported subscribers")
	}
	ch, cancel := hub.Subscribe()
	cancel()
	if _, open := <-ch; open {
		t.Fatal("nil hub handed out an open channel")
	}
}

func TestConcurrentSubscribeAndPublish(t *testing.T) {
	hub := NewHub()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ch, cancel := hub.Subscribe()
			for range ch {
				break
			}
			cancel()
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				hub.Publish(Event{Type: TypeArtistImages, Data: j})
			}
		}()
	}
	wg.Wait()
}
