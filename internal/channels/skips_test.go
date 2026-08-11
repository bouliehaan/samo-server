package channels

import (
	"testing"
	"time"
)

// A skip is a mood, not a setting: it has to wear off on its own.
func TestSkipRegistryExpires(t *testing.T) {
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	skips := NewSkipRegistry(clock)

	skips.Suppress("src1", time.Hour)
	if !skips.Suppressed("src1") {
		t.Fatal("a just-skipped source should be passed over")
	}
	if skips.Suppressed("src2") {
		t.Fatal("skipping one source must not touch another")
	}

	now = now.Add(59 * time.Minute)
	if !skips.Suppressed("src1") {
		t.Fatal("still inside the window")
	}
	now = now.Add(2 * time.Minute)
	if skips.Suppressed("src1") {
		t.Fatal("the window passed; the source should be eligible again")
	}
	// Expiry also drops the entry, so a long-lived channel cannot accumulate
	// one map key per skip forever.
	if _, still := skips.until["src1"]; still {
		t.Fatal("an expired entry should be cleared, not just ignored")
	}
}

func TestSkipRegistryClearAndNilSafety(t *testing.T) {
	skips := NewSkipRegistry(nil)
	skips.Suppress("a", time.Hour)
	skips.Suppress("b", time.Hour)
	skips.Clear([]string{"a"})
	if skips.Suppressed("a") {
		t.Fatal("cleared source should be eligible")
	}
	if !skips.Suppressed("b") {
		t.Fatal("clear must only touch what it was given")
	}

	// A nil registry is the "no skips configured" case and must not panic.
	var absent *SkipRegistry
	absent.Suppress("x", time.Hour)
	absent.Clear([]string{"x"})
	if absent.Suppressed("x") {
		t.Fatal("a nil registry suppresses nothing")
	}
	if absent.RefSuppressed("episode:x") {
		t.Fatal("a nil registry suppresses no items either")
	}
	if got := absent.PreferredSource("ch1"); got != "" {
		t.Fatalf("a nil registry has no hints, got %q", got)
	}
}

// Skipping everything must not produce silence — playing something you skipped
// an hour ago beats dead air. Suppression is a constraint like any other now,
// and the last one the engine gives up.
func TestSkipSuppressionIsRelaxedBeforeGoingSilent(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	skips := NewSkipRegistry(func() time.Time { return now })
	skips.Suppress("a", time.Hour)
	skips.Suppress("b", time.Hour)

	env := constraintEnv{now: now, skips: skips}
	candidates := []Candidate{
		{Ref: "one", SourceID: "a"},
		{Ref: "two", SourceID: "b"},
	}
	survivors, _, relaxed := applyConstraints(candidates, env)
	if len(survivors) == 0 {
		t.Fatalf("with everything skipped the station must still play something")
	}
	found := false
	for _, rule := range relaxed {
		if rule == "skipped" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the record must show that suppression was given up, got %v", relaxed)
	}
}

// And while something else is playable, a skipped source stays off.
func TestSkipSuppressionHoldsWhileThereIsAnAlternative(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	skips := NewSkipRegistry(func() time.Time { return now })
	skips.Suppress("a", time.Hour)

	env := constraintEnv{now: now, skips: skips}
	survivors, _, relaxed := applyConstraints([]Candidate{
		{Ref: "one", SourceID: "a"},
		{Ref: "two", SourceID: "b"},
	}, env)
	if len(survivors) != 1 || survivors[0].SourceID != "b" {
		t.Fatalf("expected only the unskipped source, got %+v", survivors)
	}
	if len(relaxed) != 0 {
		t.Fatalf("nothing needed relaxing, got %v", relaxed)
	}
}

// The bug this exists to prevent: SKIP and SKIP SHOW doing the same thing.
//
// The ladder orders by least-recently-aired, and the show you just skipped is
// by definition the most recently aired — so without a "stay here" hint, an
// item skip ALWAYS lands on a different show, which is precisely what SKIP
// SHOW is for.
func TestPreferredSourceIsOneShot(t *testing.T) {
	skips := NewSkipRegistry(nil)

	if got := skips.PreferredSource("ch1"); got != "" {
		t.Fatalf("no hint set, got %q", got)
	}
	skips.PreferSource("ch1", "src-podcast")

	// Reading it must NOT spend it. The preemption watchdog peeks every
	// fifteen seconds; when reading was consuming, it ate the hint four times
	// a minute and the listener's skip did nothing.
	if got := skips.PreferredSource("ch1"); got != "src-podcast" {
		t.Fatalf("expected the hint back, got %q", got)
	}
	if got := skips.PreferredSource("ch1"); got != "src-podcast" {
		t.Fatalf("peeking must not consume the hint, got %q", got)
	}

	// Only an actual pick spends it. One-shot: staying put was asked for
	// once, not made a standing order.
	skips.ClearPreferredSource("ch1")
	if got := skips.PreferredSource("ch1"); got != "" {
		t.Fatalf("hint should be spent, got %q", got)
	}

	skips.PreferSource("ch1", "a")
	if got := skips.PreferredSource("ch2"); got != "" {
		t.Fatalf("hints are per channel, got %q", got)
	}
}

// Skipping one episode must pass over that episode without touching the show,
// so the next pick is another episode of the same podcast.
func TestItemSuppressionIsSeparateFromSourceSuppression(t *testing.T) {
	skips := NewSkipRegistry(nil)
	skips.SuppressRef("episode:ep1")

	if !skips.RefSuppressed("episode:ep1") {
		t.Fatal("the skipped episode should be passed over")
	}
	if skips.RefSuppressed("episode:ep2") {
		t.Fatal("only the skipped episode, not its siblings")
	}
	// The show itself stays eligible — that is the whole difference between
	// the two buttons.
	if skips.Suppressed("src-podcast") {
		t.Fatal("skipping an episode must not suppress the show")
	}
}

// An item ref and a source id could collide as plain map keys; they must not.
func TestRefAndSourceKeysCannotCollide(t *testing.T) {
	skips := NewSkipRegistry(nil)
	skips.SuppressRef("shared-name")
	if skips.Suppressed("shared-name") {
		t.Fatal("an item ref must not suppress a source that happens to share its name")
	}
	skips2 := NewSkipRegistry(nil)
	skips2.Suppress("shared-name", time.Hour)
	if skips2.RefSuppressed("shared-name") {
		t.Fatal("a source id must not suppress an item ref that shares its name")
	}
}
