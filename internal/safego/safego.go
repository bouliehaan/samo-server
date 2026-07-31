// Package safego runs background work under a panic guard.
//
// A panic on the goroutine serving an HTTP request is contained by net/http:
// the connection dies, the process lives. A panic on any *other* goroutine is
// not contained — it takes the whole server down. samo-server runs a lot of
// background work (scans, explo passes, pollers, feed refreshes, channel
// streamers), so an unguarded `go` statement anywhere in that set turns a nil
// dereference in a metadata provider into "the music stopped".
//
// Everything launched with Go here logs its panic with a stack and returns,
// leaving the rest of the server running. That is the appliance-grade
// tradeoff: one broken subsystem should degrade, not cascade.
package safego

import (
	"runtime/debug"

	"github.com/bouliehaan/samo-server/internal/log"
)

// Go runs fn on a new goroutine under Recover. name identifies the work in the
// panic log line — use something an operator can grep for, e.g. "explo periodic
// pass" rather than "worker".
func Go(name string, fn func()) {
	go Run(name, fn)
}

// Run calls fn on the current goroutine under Recover. Use it when the caller
// already owns a goroutine (a loop body, a WaitGroup worker) and only needs the
// guard.
func Run(name string, fn func()) {
	defer Recover(name)
	fn()
}

// Recover is the deferred half of the guard, exported so callers that manage
// their own goroutine lifecycle can `defer safego.Recover("name")` directly.
// It must be called from a deferred function to have any effect.
func Recover(name string) {
	if r := recover(); r != nil {
		log.Errorf("panic recovered in %s: %v\n%s", name, r, debug.Stack())
	}
}
