package channels

import (
	"strings"
	"sync"
	"time"
)

// DefaultSkipSuppression is how long a skipped source is passed over.
//
// A skip is a mood, not a setting: "not in the mood for this podcast" should
// last the afternoon, not forever, and it should not need undoing. Deliberately
// in memory only — after a restart the station is fresh again, which is the
// right default for something you expressed by jabbing a button.
const DefaultSkipSuppression = 3 * time.Hour

// skipRefWindow is how long a skipped ITEM is passed over.
//
// Shorter than a source skip: "not this episode right now" should not cost you
// the episode for the afternoon, only for long enough that skipping does not
// hand it straight back.
const skipRefWindow = 45 * time.Minute

// skipSourceStepAside is how long SKIP steps off the show it was on.
//
// Long enough that the next pick is genuinely something else, short enough
// that it is not the three-hour "not this medium at all" of NEXT MEDIA TYPE.
const skipSourceStepAside = 20 * time.Minute

// SkipRegistry remembers what somebody skipped away from, and where they
// wanted to stay.
//
// The scheduler is otherwise a pure function of the database and the clock,
// and this is the one thing that is genuinely transient. Keeping it here, in
// memory, rather than as a column keeps "what this channel IS" and "what I did
// not want ten minutes ago" from ending up in the same place.
type SkipRegistry struct {
	mu    sync.Mutex
	until map[string]time.Time
	// prefer is a one-shot "go to this source" hint, set by BACK. Without it
	// the ordinary ordering guarantees it lands somewhere else, because what
	// just played is by definition the most recently aired thing there is.
	prefer map[string]string
	// preferRef is the same hint one level finer: the exact item BACK asked
	// for. The source is only the fallback for when that item has gone.
	preferRef map[string]string
	now       func() time.Time
}

func NewSkipRegistry(now func() time.Time) *SkipRegistry {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &SkipRegistry{
		prefer:    map[string]string{},
		preferRef: map[string]string{},
		until:     map[string]time.Time{},
		now:       now,
	}
}

// refKey namespaces item refs away from source ids so the two cannot collide.
func refKey(itemRef string) string { return "ref\x00" + itemRef }

// SuppressRef passes over one specific item — the episode you just skipped.
func (r *SkipRegistry) SuppressRef(itemRef string) {
	if r == nil || strings.TrimSpace(itemRef) == "" {
		return
	}
	r.Suppress(refKey(itemRef), skipRefWindow)
}

// RefSuppressed reports whether a specific item is being passed over.
func (r *SkipRegistry) RefSuppressed(itemRef string) bool {
	if r == nil {
		return false
	}
	return r.Suppressed(refKey(itemRef))
}

// PreferRef asks the next pick to be one SPECIFIC item.
//
// What BACK actually means. Preferring the source instead was the bug: on a
// podcast with a hundred episodes, "play the thing I just heard" narrowed the
// field to that show and then re-scored across all of it, so the button
// returned a different episode almost every time. The play log knows exactly
// which item it was; the hint has to carry that, not the show it came from.
func (r *SkipRegistry) PreferRef(channelID, itemRef string) {
	if r == nil || strings.TrimSpace(channelID) == "" || strings.TrimSpace(itemRef) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.preferRef[channelID] = itemRef
}

// PreferredRef reads the hint without consuming it.
func (r *SkipRegistry) PreferredRef(channelID string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.preferRef[channelID]
}

// ClearPreferredRef spends the hint.
func (r *SkipRegistry) ClearPreferredRef(channelID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.preferRef, channelID)
}

// PreferSource asks the next pick to stay on a source if it can.
func (r *SkipRegistry) PreferSource(channelID, sourceID string) {
	if r == nil || strings.TrimSpace(channelID) == "" || strings.TrimSpace(sourceID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prefer[channelID] = sourceID
}

// PreferredSource reads the hint without consuming it, so a speculative
// caller (the preemption watchdog, the preview endpoint) can see what would
// happen without spending it.
func (r *SkipRegistry) PreferredSource(channelID string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prefer[channelID]
}

// ClearPreferredSource spends the hint. One-shot: staying put is what the
// listener asked for once, not a new standing order.
func (r *SkipRegistry) ClearPreferredSource(channelID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.prefer, channelID)
}

// Suppress passes over a source until the window expires.
func (r *SkipRegistry) Suppress(sourceID string, window time.Duration) {
	sourceID = strings.TrimSpace(sourceID)
	if r == nil || sourceID == "" {
		return
	}
	if window <= 0 {
		window = DefaultSkipSuppression
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.until[sourceID] = r.now().Add(window)
}

// Suppressed reports whether a source is currently being passed over, and
// clears the entry once it has expired so the map cannot grow without bound.
func (r *SkipRegistry) Suppressed(sourceID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	until, ok := r.until[sourceID]
	if !ok {
		return false
	}
	if !r.now().Before(until) {
		delete(r.until, sourceID)
		return false
	}
	return true
}

// Clear forgets every suppression for a channel's sources.
func (r *SkipRegistry) Clear(sourceIDs []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range sourceIDs {
		delete(r.until, id)
	}
}

// Suppression used to be applied by filtering the source list before anything
// else looked at it, with a special case that put everything back when the
// filter emptied the pool. That is now one constraint among the others
// (`skipped`, the last one the engine will ever relax), which is strictly
// better in two ways: silence is avoided by the same mechanism that avoids it
// for every other rule, and when the station does have to play something you
// skipped an hour ago, the decision record says so instead of it looking like a
// normal choice.
