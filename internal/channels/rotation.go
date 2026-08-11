package channels

import (
	"time"
)

// What is left in this file is the part of the old rotation that was never
// about talk and music: how often one item may be repeated, and the hours in
// which airing something means anybody heard it.
//
// Everything else that used to live here — the talk/music split, the deficit
// comparison, the ninety-minute governor, the per-item length ceiling — was a
// description of one particular radio station written in Go. It now lives in
// the station's plan, where the person who owns the station can change it. See
// plan.go for the vocabulary and score.go for how a choice is actually made.

// ---- repeat limits ----------------------------------------------------

// dailyAirtimeBudget is roughly how much of a day one item may occupy across
// all its airings.
//
// This is what makes repeats safe. Airing everything the same number of times
// is wrong in a way that scales with length: three plays of a 25-minute show is
// 75 minutes and gives you three chances to catch it, three plays of a
// three-hour show is most of your waking day.
const dailyAirtimeBudget = 2 * time.Hour

// maxAiringsPerDay is how often one item may air, scaled by how long it is.
//
//	~25 min  -> 3 airings   (catch it morning, afternoon, evening)
//	~60 min  -> 2 airings
//	3 hours  -> 1 airing
//
// Always at least one: something too long for the budget still deserves to be
// heard once, or a three-hour episode could never air at all.
func maxAiringsPerDay(durationSeconds int) int {
	const cap = 3
	if durationSeconds <= 0 {
		return 1
	}
	airings := int(dailyAirtimeBudget / (time.Duration(durationSeconds) * time.Second))
	if airings < 1 {
		return 1
	}
	if airings > cap {
		return cap
	}
	return airings
}

// mayAirAgain reports whether an item has any airings left today.
//
// Only the COUNT. How soon a repeat may land is item separation's job, and that
// window adapts to how much content the station actually has — a four-hour
// repeat gap is sensible for a large library and impossible for a
// three-track one, where every track is always inside it and the rule can only
// ever be broken. Two rules that both say "not yet" with different numbers is
// how they drift apart; this one counts, the other one waits.
func mayAirAgain(durationSeconds, airedCount int) bool {
	if airedCount <= 0 {
		return true
	}
	return airedCount < maxAiringsPerDay(durationSeconds)
}

// ---- the listening day -------------------------------------------------

// ListeningDay is the window in which airing something means somebody could
// plausibly have heard it.
//
// The station runs twenty-four hours; the listener does not. Every rule about
// what is "new" is really a rule about this window, because an episode aired at
// 03:00 has not reached anyone — and the whole point of a subscription is that
// new episodes reach you. Podcasts publish overnight, so without this the
// station reliably spends the only genuinely new thing it has on a dark room
// and serves reruns to the person who wakes up at 09:17.
//
// Minutes-of-day in the channel's own timezone. Start after end means the
// window crosses midnight, which is a normal way to live.
type ListeningDay struct {
	StartMinute int
	EndMinute   int
}

// DefaultListeningDay is 08:00 to 23:00.
var DefaultListeningDay = ListeningDay{StartMinute: 8 * 60, EndMinute: 23 * 60}

func (d ListeningDay) normalized() ListeningDay {
	if d.StartMinute == d.EndMinute {
		return DefaultListeningDay
	}
	if d.StartMinute < 0 || d.StartMinute > 1439 || d.EndMinute < 0 || d.EndMinute > 1439 {
		return DefaultListeningDay
	}
	return d
}

// Contains reports whether a wall-clock time falls inside the listening day.
func (d ListeningDay) Contains(at time.Time) bool {
	day := d.normalized()
	minute := at.Hour()*60 + at.Minute()
	if day.StartMinute <= day.EndMinute {
		return minute >= day.StartMinute && minute < day.EndMinute
	}
	// Crosses midnight: awake late, asleep in the small hours.
	return minute >= day.StartMinute || minute < day.EndMinute
}

// NextStart is when the listening day next begins, at or after `at`.
//
// Built with wallClock rather than midnight-plus-a-duration: on the day the
// clocks change, adding eight hours to midnight does not give you 08:00, and a
// station that quietly holds every overnight release an hour too long twice a
// year is impossible to debug from the listening end.
func (d ListeningDay) NextStart(at time.Time) time.Time {
	day := d.normalized()
	loc := at.Location()
	start := wallClock(startOfDay(at, loc), day.StartMinute, loc)
	if !start.After(at) {
		start = wallClock(startOfDay(at, loc).AddDate(0, 0, 1), day.StartMinute, loc)
	}
	return start
}

// holdForListeningDay reports whether a new release should wait rather than be
// spent on an empty room.
//
// Only ever holds something that will still be new when the day starts. An
// episode is not held forever waiting for a perfect moment: if the wait would
// outlast its freshness there is no morning left to save it for, and airing it
// now is strictly better than never airing it.
func holdForListeningDay(published, now time.Time, day ListeningDay, freshFor time.Duration) bool {
	if day.Contains(now) {
		return false
	}
	return day.NextStart(now).Before(published.Add(freshFor))
}
