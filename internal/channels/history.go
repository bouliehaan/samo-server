package channels

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// History is everything the scheduler knows about what this station has
// already done.
//
// It is an interface for one reason that turns out to matter enormously: the
// simulator. Every rule about balance, repeats, separation and freshness is a
// rule about history, so a scheduler wired directly to a play-log table can
// only ever be observed by listening to it. Behind an interface, the same
// decision code runs against an in-memory history and a virtual clock, and
// three days of programming can be inspected in a second without putting a
// single byte on the air.
type History interface {
	// Airtime is how much of a window each source and each category filled,
	// measured by interval overlap.
	Airtime(ctx context.Context, window time.Duration, now time.Time) (AirtimeWindow, error)
	// LastAiredBySource is when this station last played anything from each
	// source.
	LastAiredBySource(ctx context.Context, window time.Duration, now time.Time) (map[string]time.Time, error)
	// LastAiredByRef is when it last played each specific item.
	LastAiredByRef(ctx context.Context, window time.Duration, now time.Time) (map[string]time.Time, error)
	// LastLongFormBySource is when each source last put an enormous item on
	// air, which is the event that earns a show its rest.
	LastLongFormBySource(ctx context.Context, minDuration, window time.Duration, now time.Time) (map[string]LongFormAiring, error)
	// Tail is the recent running order, newest first. Aggregates cannot answer
	// questions about runs, and a run is what a listener actually experiences.
	Tail(ctx context.Context, window time.Duration, limit int, now time.Time) ([]PlayTailEntry, error)
	// ItemAirings counts how often each item aired in a window and when it last
	// did, for the repeat caps.
	ItemAirings(ctx context.Context, window time.Duration, now time.Time) (map[string]int, map[string]time.Time, error)
	// AiredInListeningDay counts only the airings that happened while somebody
	// could plausibly have been listening.
	AiredInListeningDay(ctx context.Context, window time.Duration, day ListeningDay, loc *time.Location, now time.Time) (map[string]int, map[string]time.Time, error)
}

// sqlHistory is the real station's memory.
type sqlHistory struct {
	db        *sql.DB
	channelID string
}

// NewSQLHistory reads a channel's play log.
func NewSQLHistory(db *sql.DB, channelID string) History {
	return &sqlHistory{db: db, channelID: channelID}
}

func (h *sqlHistory) Airtime(ctx context.Context, window time.Duration, now time.Time) (AirtimeWindow, error) {
	return AirtimeBySource(ctx, h.db, h.channelID, window, now)
}

func (h *sqlHistory) LastAiredBySource(ctx context.Context, window time.Duration, now time.Time) (map[string]time.Time, error) {
	return LastAiredBySource(ctx, h.db, h.channelID, window, now)
}

func (h *sqlHistory) LastLongFormBySource(ctx context.Context, minDuration, window time.Duration, now time.Time) (map[string]LongFormAiring, error) {
	return LastLongFormBySource(ctx, h.db, h.channelID, minDuration, window, now)
}

func (h *sqlHistory) LastAiredByRef(ctx context.Context, window time.Duration, now time.Time) (map[string]time.Time, error) {
	return LastAiredByRef(ctx, h.db, h.channelID, window, now)
}

func (h *sqlHistory) Tail(ctx context.Context, window time.Duration, limit int, now time.Time) ([]PlayTailEntry, error) {
	return PlayLogTail(ctx, h.db, h.channelID, window, limit, now)
}

func (h *sqlHistory) ItemAirings(ctx context.Context, window time.Duration, now time.Time) (map[string]int, map[string]time.Time, error) {
	return ItemAirings(ctx, h.db, h.channelID, window, now)
}

func (h *sqlHistory) AiredInListeningDay(ctx context.Context, window time.Duration, day ListeningDay, loc *time.Location, now time.Time) (map[string]int, map[string]time.Time, error) {
	return AiredInListeningDay(ctx, h.db, h.channelID, window, day, loc, now)
}

// ---- in-memory history -------------------------------------------------

// MemoryHistory is a play log that never touches a database.
//
// Used by the simulator, and by tests that want to state a station's past in
// three lines instead of seeding rows. It answers the same questions the same
// way — in particular it measures airtime by OVERLAP with the window, because a
// history that only counts items which started inside the window loses exactly
// the long blocks that dominate it.
type MemoryHistory struct {
	entries []MemoryPlay
}

// MemoryPlay is one thing that aired.
type MemoryPlay struct {
	SourceID  string
	ItemRef   string
	Artist    string
	Category  CategoryID
	StartedAt time.Time
	EndedAt   time.Time
	// DurationSeconds is the fallback when the clock says nothing useful, the
	// same as the stored column.
	DurationSeconds int
}

// NewMemoryHistory builds an empty in-memory history.
func NewMemoryHistory() *MemoryHistory { return &MemoryHistory{} }

// Record appends a play. Later reads see it immediately, which is what lets the
// simulator feed its own decisions back in.
func (h *MemoryHistory) Record(play MemoryPlay) {
	if play.Category == "" {
		play.Category = LegacyCategoryTalk
	}
	h.entries = append(h.entries, play)
}

// Len is how many plays are remembered.
func (h *MemoryHistory) Len() int { return len(h.entries) }

// Plays is everything recorded, oldest first, for reporting.
func (h *MemoryHistory) Plays() []MemoryPlay {
	return append([]MemoryPlay(nil), h.entries...)
}

func (h *MemoryHistory) Airtime(_ context.Context, window time.Duration, now time.Time) (AirtimeWindow, error) {
	out := AirtimeWindow{
		BySource:   map[string]SourceAirtime{},
		ByCategory: map[CategoryID]time.Duration{},
	}
	if window <= 0 {
		window = 24 * time.Hour
	}
	start := now.Add(-window)
	for _, entry := range h.entries {
		aired := airedDuration(entry.StartedAt, entry.EndedAt, int64(entry.DurationSeconds), start, now)
		if aired <= 0 || entry.StartedAt.After(now) {
			continue
		}
		if !entry.EndedAt.IsZero() && !entry.EndedAt.After(start) {
			continue
		}
		bucket := out.BySource[entry.SourceID]
		bucket.SourceID = entry.SourceID
		bucket.Aired += aired
		bucket.Plays++
		if bucket.ByCategory == nil {
			bucket.ByCategory = map[CategoryID]time.Duration{}
		}
		bucket.ByCategory[entry.Category] += aired
		out.BySource[entry.SourceID] = bucket
		out.ByCategory[entry.Category] += aired
		out.Total += aired
	}
	return out, nil
}

func (h *MemoryHistory) LastAiredBySource(_ context.Context, window time.Duration, now time.Time) (map[string]time.Time, error) {
	return h.lastAired(window, now, func(entry MemoryPlay) string { return entry.SourceID }), nil
}

func (h *MemoryHistory) LastAiredByRef(_ context.Context, window time.Duration, now time.Time) (map[string]time.Time, error) {
	return h.lastAired(window, now, func(entry MemoryPlay) string { return entry.ItemRef }), nil
}

// LastLongFormBySource mirrors the SQL query: the last time each source put
// something enormous on air, measured from when that item finished.
func (h *MemoryHistory) LastLongFormBySource(_ context.Context, minDuration, window time.Duration, now time.Time) (map[string]LongFormAiring, error) {
	out := map[string]LongFormAiring{}
	if minDuration <= 0 {
		return out, nil
	}
	cutoff := now.Add(-window)
	for _, entry := range h.entries {
		if entry.SourceID == "" || entry.StartedAt.Before(cutoff) || entry.StartedAt.After(now) {
			continue
		}
		length := time.Duration(entry.DurationSeconds) * time.Second
		if length <= 0 && !entry.EndedAt.IsZero() {
			length = entry.EndedAt.Sub(entry.StartedAt)
		}
		if length < minDuration {
			continue
		}
		if ended := entry.StartedAt.Add(length); ended.After(out[entry.SourceID].EndedAt) {
			out[entry.SourceID] = LongFormAiring{EndedAt: ended, Length: length}
		}
	}
	return out, nil
}

func (h *MemoryHistory) lastAired(window time.Duration, now time.Time, key func(MemoryPlay) string) map[string]time.Time {
	out := map[string]time.Time{}
	cutoff := now.Add(-window)
	for _, entry := range h.entries {
		id := key(entry)
		if id == "" || entry.StartedAt.Before(cutoff) || entry.StartedAt.After(now) {
			continue
		}
		if existing, ok := out[id]; !ok || entry.StartedAt.After(existing) {
			out[id] = entry.StartedAt
		}
	}
	return out
}

func (h *MemoryHistory) Tail(_ context.Context, window time.Duration, limit int, now time.Time) ([]PlayTailEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	cutoff := now.Add(-window)
	out := make([]PlayTailEntry, 0, limit)
	for index := len(h.entries) - 1; index >= 0 && len(out) < limit; index-- {
		entry := h.entries[index]
		if entry.StartedAt.After(now) {
			continue
		}
		// Overlapping, not started-inside: the block that put a run over the
		// line is usually the one that began before the window did.
		if entry.StartedAt.Before(cutoff) && !entry.EndedAt.IsZero() && entry.EndedAt.Before(cutoff) {
			continue
		}
		out = append(out, PlayTailEntry{
			SourceID:  entry.SourceID,
			ItemRef:   entry.ItemRef,
			Artist:    entry.Artist,
			Category:  entry.Category,
			StartedAt: entry.StartedAt,
			Aired:     airedDuration(entry.StartedAt, entry.EndedAt, int64(entry.DurationSeconds), cutoff, now),
		})
	}
	return out, nil
}

func (h *MemoryHistory) ItemAirings(_ context.Context, window time.Duration, now time.Time) (map[string]int, map[string]time.Time, error) {
	counts := map[string]int{}
	last := map[string]time.Time{}
	cutoff := now.Add(-window)
	for _, entry := range h.entries {
		if entry.ItemRef == "" || entry.StartedAt.Before(cutoff) || entry.StartedAt.After(now) {
			continue
		}
		counts[entry.ItemRef]++
		if existing, ok := last[entry.ItemRef]; !ok || entry.StartedAt.After(existing) {
			last[entry.ItemRef] = entry.StartedAt
		}
	}
	return counts, last, nil
}

func (h *MemoryHistory) AiredInListeningDay(_ context.Context, window time.Duration, day ListeningDay, loc *time.Location, now time.Time) (map[string]int, map[string]time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	counts := map[string]int{}
	last := map[string]time.Time{}
	cutoff := now.Add(-window)
	for _, entry := range h.entries {
		if entry.ItemRef == "" || entry.StartedAt.Before(cutoff) || entry.StartedAt.After(now) {
			continue
		}
		if !day.Contains(entry.StartedAt.In(loc)) {
			continue
		}
		counts[entry.ItemRef]++
		if existing, ok := last[entry.ItemRef]; !ok || entry.StartedAt.After(existing) {
			last[entry.ItemRef] = entry.StartedAt
		}
	}
	return counts, last, nil
}

// ---- derived views -----------------------------------------------------

// ExcludingSources removes some sources from the category totals.
//
// Interstitial inventory is the case: a station ident and a spot are things
// that go BETWEEN programming, and counting their airtime toward a category's
// share means a station with a big spot pool believes it has been playing more
// talk than it has.
func (w AirtimeWindow) ExcludingSources(ids map[string]bool) AirtimeWindow {
	if len(ids) == 0 {
		return w
	}
	out := AirtimeWindow{
		BySource:   map[string]SourceAirtime{},
		ByCategory: map[CategoryID]time.Duration{},
		Total:      w.Total,
	}
	for id, entry := range w.BySource {
		out.BySource[id] = entry
	}
	for category, aired := range w.ByCategory {
		out.ByCategory[category] = aired
	}
	for id := range ids {
		entry, ok := w.BySource[id]
		if !ok {
			continue
		}
		// Subtracted from the bucket each airing actually went into, not from
		// the one the source would be filed under today: a source that has been
		// re-labelled since would otherwise leave its old airtime stuck in a
		// category nothing can take it out of.
		for category, aired := range entry.ByCategory {
			remaining := out.ByCategory[category] - aired
			if remaining < 0 {
				remaining = 0
			}
			out.ByCategory[category] = remaining
		}
		out.Total -= entry.Aired
		if out.Total < 0 {
			out.Total = 0
		}
	}
	return out
}

// CategoryRun measures the unbroken run of one category at the end of the tail.
//
// Generic over the category on purpose. The old engine had one of these, hard
// coded to spoken word, because that was the only category it knew existed —
// but "how long have we been doing the same kind of thing" is a question about
// any category a station defines, and a station of comedy and jazz has exactly
// the same problem.
//
// `resetAfter` is what stops a single short interlude clearing a long run: one
// three-minute track between two hour-long items is not a break, and without a
// floor the run counter resets and the limit above it never fires.
// `since` bounds the run to the current block. A limit belongs to a block, so
// the run it measures has to be the run INSIDE that block: a booked news hour
// is an appointment its owner asked for, and letting it count toward the
// general rotation's "don't talk for more than ninety minutes" means the
// rotation is punished for the appointment and cannot play spoken word
// afterwards — it has to dump music until the clock forgives it. That is
// measuring one block's taste against another block's airtime, which is the
// same category of mistake as comparing per-source deficits across categories.
func CategoryRun(tail []PlayTailEntry, category CategoryID, resetAfter time.Duration, since time.Time) time.Duration {
	var run, other time.Duration
	for _, entry := range tail {
		if !since.IsZero() && entry.StartedAt.Before(since) {
			// Anything that began BEFORE this block did belongs to whatever was
			// on air then.
			//
			// Strictly before. A block's own first item starts at the very
			// instant the block is entered, so excluding items that start at
			// `since` threw away one item from every run — the limit then
			// measured the run minus its first item, permanently lagged by one,
			// and a ninety-minute ceiling let a hundred-and-thirty-minute run
			// through before it noticed.
			return run
		}
		if entry.Category == category {
			run += entry.Aired
			other = 0
			continue
		}
		other += entry.Aired
		if resetAfter > 0 && other >= resetAfter {
			return run
		}
		if resetAfter <= 0 {
			return run
		}
	}
	return run
}

// typicalAired is the median length of what this station has recently played.
//
// Measured rather than configured: a talk channel's norm is forty minutes and a
// music channel's is three, and neither should have to write that down. It is
// what "this item is a big commitment" is relative to.
func typicalAired(tail []PlayTailEntry) time.Duration {
	lengths := make([]time.Duration, 0, len(tail))
	for _, entry := range tail {
		if entry.Aired > 0 {
			lengths = append(lengths, entry.Aired)
		}
	}
	if len(lengths) == 0 {
		return 0
	}
	sort.Slice(lengths, func(i, j int) bool { return lengths[i] < lengths[j] })
	return lengths[len(lengths)/2]
}
