package safego_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/safego"
)

func TestGroupWaitsForWorkers(t *testing.T) {
	var group safego.Group
	var finished atomic.Int32

	for i := 0; i < 8; i++ {
		group.Go("worker", func() {
			time.Sleep(10 * time.Millisecond)
			finished.Add(1)
		})
	}

	if !group.Wait(5 * time.Second) {
		t.Fatal("Wait timed out")
	}
	if got := finished.Load(); got != 8 {
		t.Fatalf("Wait returned before workers finished: %d/8", got)
	}
}

// A panicking worker must still release its slot, or shutdown would block for
// the full drain timeout every time one misbehaved.
func TestGroupWaitCompletesWhenAWorkerPanics(t *testing.T) {
	var group safego.Group
	group.Go("panicker", func() { panic("boom") })

	if !group.Wait(5 * time.Second) {
		t.Fatal("a panicking worker left the group waiting")
	}
}

// The reason Group exists rather than a bare sync.WaitGroup: scan-completion
// callbacks start workers at arbitrary times, so a start can race the shutdown
// wait. A raw WaitGroup panics with "reused before previous Wait has returned";
// this must simply decline the late work.
func TestGroupToleratesGoRacingWait(t *testing.T) {
	for attempt := 0; attempt < 200; attempt++ {
		var group safego.Group
		var wg sync.WaitGroup

		wg.Add(2)
		go func() {
			defer wg.Done()
			group.Go("late worker", func() {})
		}()
		go func() {
			defer wg.Done()
			group.Wait(5 * time.Second)
		}()
		wg.Wait()
	}
}

func TestGroupDeclinesWorkAfterWait(t *testing.T) {
	var group safego.Group
	group.Wait(time.Second)

	started := make(chan struct{})
	group.Go("post-shutdown worker", func() { close(started) })

	select {
	case <-started:
		t.Fatal("group started work after shutdown began")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestGroupWaitReportsTimeout(t *testing.T) {
	var group safego.Group
	release := make(chan struct{})
	group.Go("slow worker", func() { <-release })

	if group.Wait(50 * time.Millisecond) {
		t.Fatal("Wait claimed success while a worker was still running")
	}
	close(release)
}
