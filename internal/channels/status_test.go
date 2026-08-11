package channels

import (
	"testing"
	"time"
)

// The "next slot" readout has to agree with the matcher about what day and
// minute it is, or the panel would reassure somebody while the show fails.
func TestNextRuleAfterFindsTheNextSlotToday(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("tz database unavailable: %v", err)
	}
	// A Wednesday.
	at := time.Date(2026, 8, 5, 14, 30, 0, 0, denver)
	weekdayBit := 1 << int(at.Weekday())

	rules := []ScheduleRule{
		{ID: "morning", Enabled: true, WeekdayMask: 127, StartMinute: 6 * 60, EndMinute: 7 * 60},
		{ID: "krdo", Enabled: true, WeekdayMask: 127, StartMinute: 23 * 60, EndMinute: 24 * 60},
		{ID: "atc", Enabled: true, WeekdayMask: weekdayBit, StartMinute: 16 * 60, EndMinute: 17 * 60},
		{ID: "weekend", Enabled: true, WeekdayMask: 65, StartMinute: 15 * 60, EndMinute: 16 * 60},
		{ID: "off", Enabled: false, WeekdayMask: 127, StartMinute: 15 * 60, EndMinute: 16 * 60},
	}

	next, startsAt, ok := nextRuleAfter(rules, at)
	if !ok {
		t.Fatal("there are later slots today")
	}
	// Not the weekend rule (wrong day), not the disabled one, not this
	// morning's (already gone) — the 16:00 one.
	if next.ID != "atc" {
		t.Fatalf("expected the 16:00 slot, got %q", next.ID)
	}
	if startsAt.Format("15:04") != "16:00" {
		t.Fatalf("expected a 16:00 start, got %s", startsAt.Format("15:04"))
	}
	if startsAt.Location() != denver {
		t.Fatalf("the start time must be in the channel's zone, got %v", startsAt.Location())
	}

	// After the last slot of the day there is simply nothing more today.
	if _, _, ok := nextRuleAfter(rules, time.Date(2026, 8, 5, 23, 30, 0, 0, denver)); ok {
		t.Fatal("nothing is due after the last slot starts")
	}
}

// A 23:00–24:00 slot is the exact shape that prompted this: it must be active
// at 23:30 and not at 22:59.
func TestLateNightSlotIsActiveInItsHour(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("tz database unavailable: %v", err)
	}
	rules := []ScheduleRule{{
		ID: "krdo", SourceID: "src", Enabled: true,
		WeekdayMask: 127, StartMinute: 23 * 60, EndMinute: 24 * 60,
	}}
	if _, ok := pickActiveRule(rules, time.Date(2026, 8, 5, 23, 30, 0, 0, denver)); !ok {
		t.Fatal("23:30 is inside a 23:00-24:00 slot")
	}
	if _, ok := pickActiveRule(rules, time.Date(2026, 8, 5, 22, 59, 0, 0, denver)); ok {
		t.Fatal("22:59 is before it")
	}
	if _, ok := pickActiveRule(rules, time.Date(2026, 8, 6, 0, 1, 0, 0, denver)); ok {
		t.Fatal("00:01 the next day is after it")
	}
}

// "Local" is what time.Local stringifies to, and it is the least useful thing
// the panel could say: it hides that the schedule is being read in UTC, which
// is the single most common reason a slot does not fire.
func TestZoneNameNeverReportsLocal(t *testing.T) {
	utcNow := time.Date(2026, 8, 9, 5, 54, 0, 0, time.UTC)
	if got := zoneName(time.Local, utcNow); got == "Local" {
		t.Fatalf("zoneName must resolve to something actionable, got %q", got)
	}
	if got := zoneName(time.UTC, utcNow); got != "UTC" {
		t.Fatalf("expected UTC, got %q", got)
	}
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("tz database unavailable: %v", err)
	}
	if got := zoneName(denver, utcNow.In(denver)); got != "America/Denver" {
		t.Fatalf("a named zone reports its name, got %q", got)
	}
}

// The exact readout from the bug report: 05:54 Sunday UTC is 23:54 Saturday in
// Denver, so a Saturday-night slot looks like it is not booked for "today".
func TestUTCShiftsTheWeekdayNotJustTheHour(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("tz database unavailable: %v", err)
	}
	utcNow := time.Date(2026, 8, 9, 5, 54, 0, 0, time.UTC)
	local := utcNow.In(denver)

	if utcNow.Weekday() != time.Sunday || local.Weekday() != time.Saturday {
		t.Fatalf("setup wrong: utc=%v local=%v", utcNow.Weekday(), local.Weekday())
	}

	// A Saturday-only 23:00-24:00 slot.
	saturdayOnly := 1 << int(time.Saturday)
	rules := []ScheduleRule{{
		ID: "krcc", SourceID: "src", Enabled: true,
		WeekdayMask: saturdayOnly, StartMinute: 23 * 60, EndMinute: 24 * 60,
	}}

	if _, ok := pickActiveRule(rules, local); !ok {
		t.Fatal("the slot is open at 23:54 Saturday local")
	}
	// Read in UTC it is Sunday, so the weekday mask alone rules it out — the
	// hour never even gets compared. That is why it reported "no slot open".
	if _, ok := pickActiveRule(rules, utcNow); ok {
		t.Fatal("guard: read as UTC this is Sunday and must not match")
	}
}
