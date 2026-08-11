package channels

import (
	"sort"
	"time"
)

// The timeline is what the station knows about its own future.
//
// The old scheduler could see thirty minutes ahead, today only, and only at
// slots that had not started yet — and it used that answer for nothing except
// capping live streams. So a ninety-minute podcast could start twenty minutes
// before a booked show and simply be cut off mid-sentence, which is not a
// scheduling decision, it is a collision. Everything here exists so that
// "what is booked, and how much room is there before it" is a question with a
// real answer that every candidate is measured against.

// AnchorHorizon is how far ahead anchors are resolved.
//
// Two days rather than "the rest of today", because at 23:50 the interesting
// appointment is tomorrow morning's, and a scheduler that cannot see past
// midnight will happily start a four-hour block in front of it.
const AnchorHorizon = 48 * time.Hour

// Anchor is one occurrence of a hard-scheduled block on the timeline.
type Anchor struct {
	BlockID string
	Label   string
	Start   time.Time
	// End is when the anchor's own window closes. An anchor that does not say
	// runs until the next one starts, which is what a booked slot with no
	// stated end means on paper.
	End    time.Time
	Policy StartPolicy
	Grace  time.Duration
}

// Contains reports whether an anchor's window covers a moment.
func (a Anchor) Contains(at time.Time) bool {
	if at.Before(a.Start) {
		return false
	}
	if a.End.IsZero() {
		return false
	}
	return at.Before(a.End)
}

// Timeline is the resolved schedule around a moment.
type Timeline struct {
	Now      time.Time
	Location *time.Location
	Anchors  []Anchor
	// Active is the anchor whose window covers Now.
	Active *Anchor
	// Next is the soonest anchor starting after Now.
	Next *Anchor
}

// Window is how long the station has before the next appointment.
//
// Zero means unbounded — nothing is booked inside the horizon — and every
// caller has to treat that as "no limit" rather than "no time", which is why it
// is a named method rather than a bare subtraction at each call site.
func (t Timeline) Window() time.Duration {
	if t.Next == nil {
		return 0
	}
	window := t.Next.Start.Sub(t.Now)
	if window < 0 {
		return 0
	}
	return window
}

// BuildTimeline resolves every hard-anchored block in the plan into concrete
// occurrences around `now`.
//
// Wall-clock times are constructed with time.Date rather than by adding minutes
// to midnight. On a day the clock changes, midnight plus eight hours is not
// 08:00, and the old code made exactly that mistake in three places — which is
// how a show fires an hour late twice a year and nobody can reproduce it.
func BuildTimeline(plan Plan, now time.Time, loc *time.Location) Timeline {
	if loc == nil {
		loc = time.UTC
	}
	now = now.In(loc)
	timeline := Timeline{Now: now, Location: loc}

	// A day either side of the horizon, so a window that began yesterday and
	// runs past midnight is still visible.
	from := now.AddDate(0, 0, -1)
	to := now.Add(AnchorHorizon)

	for _, block := range plan.Blocks {
		if !block.Enter.Hard || block.Enter.At == "" {
			continue
		}
		startMinute, err := parseClock(block.Enter.At)
		if err != nil {
			continue
		}
		mask, err := parseWeekdays(block.Enter.Days)
		if err != nil {
			mask = 127
		}
		grace, _ := parseDuration(block.Enter.Grace)
		policy := block.Enter.Start
		if policy == "" {
			policy = StartMakeNext
		}

		for day := startOfDay(from, loc); !day.After(to); day = day.AddDate(0, 0, 1) {
			if mask&(1<<int(day.Weekday())) == 0 {
				continue
			}
			start := wallClock(day, startMinute, loc)
			end := anchorEnd(block, start, loc)
			if end.After(from) || start.After(from) {
				timeline.Anchors = append(timeline.Anchors, Anchor{
					BlockID: block.ID,
					Label:   firstNonEmpty(block.Label, block.ID),
					Start:   start,
					End:     end,
					Policy:  policy,
					Grace:   grace,
				})
			}
		}
	}

	sort.SliceStable(timeline.Anchors, func(i, j int) bool {
		if !timeline.Anchors[i].Start.Equal(timeline.Anchors[j].Start) {
			return timeline.Anchors[i].Start.Before(timeline.Anchors[j].Start)
		}
		return timeline.Anchors[i].BlockID < timeline.Anchors[j].BlockID
	})

	// An anchor with no stated end runs until the next one begins. Resolved
	// here, after sorting, because it is a fact about the schedule as a whole
	// rather than about any one block.
	for index := range timeline.Anchors {
		if !timeline.Anchors[index].End.IsZero() {
			continue
		}
		if index+1 < len(timeline.Anchors) {
			timeline.Anchors[index].End = timeline.Anchors[index+1].Start
			continue
		}
		timeline.Anchors[index].End = timeline.Anchors[index].Start.AddDate(0, 0, 1)
	}

	for index := range timeline.Anchors {
		anchor := timeline.Anchors[index]
		if anchor.Contains(now) && timeline.Active == nil {
			timeline.Active = &timeline.Anchors[index]
		}
		if anchor.Start.After(now) && timeline.Next == nil {
			timeline.Next = &timeline.Anchors[index]
		}
	}
	return timeline
}

// anchorEnd works out when a hard block's window closes, from whichever exit it
// declares. Zero means "not stated" and is resolved against the next anchor.
func anchorEnd(block Block, start time.Time, loc *time.Location) time.Time {
	if block.Exit.At != "" {
		if endMinute, err := parseClock(block.Exit.At); err == nil {
			end := wallClock(startOfDay(start, loc), endMinute, loc)
			if !end.After(start) {
				// The window crosses midnight, which is a normal way to
				// programme a night block.
				end = wallClock(startOfDay(start, loc).AddDate(0, 0, 1), endMinute, loc)
			}
			return end
		}
	}
	if block.Exit.Duration != "" {
		if duration, err := parseDuration(block.Exit.Duration); err == nil && duration > 0 {
			return start.Add(duration)
		}
	}
	return time.Time{}
}

// startOfDay is local midnight for a moment's calendar date.
func startOfDay(at time.Time, loc *time.Location) time.Time {
	at = at.In(loc)
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
}

// wallClock builds "this calendar day, at this minute of the day" as a real
// instant.
//
// time.Date does the right thing across a clock change: on a spring-forward day
// a local time that does not exist is normalised forward rather than silently
// landing an hour out, and on a fall-back day the first of the two possible
// instants is chosen. Both are defensible; both are stable; neither is what
// midnight-plus-a-duration does.
func wallClock(day time.Time, minuteOfDay int, loc *time.Location) time.Time {
	day = day.In(loc)
	return time.Date(day.Year(), day.Month(), day.Day(), minuteOfDay/60, minuteOfDay%60, 0, 0, loc)
}

// FitWindow is how long an item may run if it must finish before the next
// appointment. Zero means no limit.
func (t Timeline) FitWindow() time.Duration { return t.Window() }

// AnchorFor returns the anchor covering a moment, if any.
func (t Timeline) AnchorFor(at time.Time) *Anchor {
	for index := range t.Anchors {
		if t.Anchors[index].Contains(at) {
			return &t.Anchors[index]
		}
	}
	return nil
}

// nextLabel names whatever is booked next, for the decision record.
func (t Timeline) nextLabel() string {
	if t.Next == nil {
		return "the next booked block"
	}
	if t.Next.Label != "" {
		return t.Next.Label
	}
	return t.Next.BlockID
}
