package serverid

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestEnsureMintsAnIDOnFirstCall(t *testing.T) {
	db := storagetest.Open(t)

	id, err := Ensure(context.Background(), db)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	if !strings.HasPrefix(id, Prefix) {
		t.Fatalf("expected %q prefix, got %q", Prefix, id)
	}
	if len(id) != len(Prefix)+idBytes*2 {
		t.Fatalf("unexpected id length: %q", id)
	}
}

// The whole point of the ID is that it never changes -- a new ID would detach
// every client from its locally cached catalog and downloads.
func TestEnsureIsStableAcrossCalls(t *testing.T) {
	db := storagetest.Open(t)
	ctx := context.Background()

	first, err := Ensure(ctx, db)
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	second, err := Ensure(ctx, db)
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}

	if first != second {
		t.Fatalf("identity changed between calls: %q then %q", first, second)
	}
}

// Boot paths can race (HTTP server start, discovery broadcaster, health probe).
// Every racer must observe the same identity, not just avoid an error.
func TestEnsureIsSafeUnderConcurrentCalls(t *testing.T) {
	db := storagetest.Open(t)
	ctx := context.Background()

	const racers = 8
	results := make([]string, racers)
	errs := make([]error, racers)

	var wg sync.WaitGroup
	wg.Add(racers)
	for i := range racers {
		go func(slot int) {
			defer wg.Done()
			results[slot], errs[slot] = Ensure(ctx, db)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
	}
	for i, got := range results {
		if got != results[0] {
			t.Fatalf("racer %d saw %q, racer 0 saw %q", i, got, results[0])
		}
	}
}

func TestEnsureRejectsNilDatabase(t *testing.T) {
	if _, err := Ensure(context.Background(), nil); err == nil {
		t.Fatal("expected an error for a nil database")
	}
}
