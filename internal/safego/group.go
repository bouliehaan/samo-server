package safego

import (
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/log"
)

// Group tracks panic-guarded background workers so shutdown can wait for them
// to unwind before tearing down what they depend on — most importantly the
// database pool, which a scan or an enrichment pass may be mid-write against.
//
// It is not a bare sync.WaitGroup because workers here are started by events,
// not just at boot: a scan finishing fires callbacks that launch more work, and
// that can happen at any moment including during shutdown. sync.WaitGroup
// forbids an Add that races a Wait — it can return early or panic with
// "WaitGroup is reused before previous Wait has returned" — so Group closes to
// new work at the moment the wait begins.
type Group struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
}

// Go starts fn under the panic guard and tracks it, unless the group has
// already begun shutting down — in which case the work is declined and logged,
// since its context is cancelled anyway.
func (g *Group) Go(name string, fn func()) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		log.Debugf("not starting background worker %q: shutting down", name)
		return
	}
	g.wg.Add(1)
	g.mu.Unlock()

	Go(name, func() {
		defer g.wg.Done()
		fn()
	})
}

// Wait closes the group to new work and blocks until the running workers
// return, or until timeout elapses. A wedged worker should delay shutdown, not
// prevent it, so a timeout is reported and not treated as fatal.
//
// Returns true if every worker finished within the timeout.
func (g *Group) Wait(timeout time.Duration) bool {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()

	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		log.Warnf("background workers still running after %s; continuing shutdown anyway", timeout)
		return false
	}
}
