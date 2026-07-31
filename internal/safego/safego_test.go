package safego_test

import (
	"sync"
	"testing"

	"github.com/bouliehaan/samo-server/internal/safego"
)

// TestGoSurvivesPanic is the whole point of the package: a panicking background
// goroutine must not take the process with it. If the guard regresses, this
// test crashes the test binary rather than failing, which is the correct
// signal — that is exactly what would happen to the server.
func TestGoSurvivesPanic(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	safego.Go("test panicker", func() {
		defer wg.Done()
		panic("boom")
	})
	wg.Wait()
}

func TestRunSurvivesPanicAndReturns(t *testing.T) {
	reached := false
	safego.Run("test panicker", func() { panic("boom") })
	reached = true
	if !reached {
		t.Fatal("Run did not return after recovering")
	}
}

func TestRunPassesThroughNormalCompletion(t *testing.T) {
	ran := false
	safego.Run("test worker", func() { ran = true })
	if !ran {
		t.Fatal("fn did not run")
	}
}
