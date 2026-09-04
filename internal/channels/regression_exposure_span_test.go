package channels

import (
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Exposure is a property of a SPAN, and the station was sampling it at a point.
//
// `item.Exposure` was read from the clock at the instant an item was chosen and
// then applied to the whole airing. For anything short that is the same answer;
// for the long episodes a station actually schedules around it is the
// difference between the two things that cannot both be true, and which the
// morning of 2026-09-03 reported at once: an episode still owed with credit 0%,
// and the same episode's show refused for having "aired 0s ago".
//
// An episode running 07:00–09:12 against an 08:00 listening day plays
// seventy-two of its hundred and thirty-two minutes to somebody who is awake.
// Asked only about 07:00, the station recorded that as reaching nobody.

func TestListeningDayOverlapMeasuresTheSpan(t *testing.T) {
	day := ListeningDay{StartMinute: 8 * 60, EndMinute: 23 * 60}
	at := func(h, m int) time.Time { return time.Date(2026, 9, 3, h, m, 0, 0, time.UTC) }

	cases := []struct {
		name     string
		from, to time.Time
		want     time.Duration
	}{
		{"wholly inside", at(9, 0), at(10, 0), time.Hour},
		{"wholly outside", at(3, 0), at(4, 0), 0},
		{"the reported shape: starts before the day, ends inside it",
			at(7, 0), at(9, 12), 72 * time.Minute},
		{"runs out past the end of the day", at(22, 30), at(23, 30), 30 * time.Minute},
		{"straddles the whole day", at(7, 0), at(23, 30), 15 * time.Hour},
		{"zero length", at(9, 0), at(9, 0), 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := day.Overlap(tc.from, tc.to); got != tc.want {
				t.Fatalf("%s → %s overlapped %s, want %s",
					tc.from.Format("15:04"), tc.to.Format("15:04"), got, tc.want)
			}
		})
	}
}

// A listening day that crosses midnight is a normal way to live, and the walk
// has to open a calendar day early to see the window it belongs to.
func TestListeningDayOverlapAcrossMidnight(t *testing.T) {
	// Awake 20:00 to 02:00.
	day := ListeningDay{StartMinute: 20 * 60, EndMinute: 2 * 60}

	from := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 3, 3, 0, 0, 0, time.UTC)
	if got := day.Overlap(from, to); got != time.Hour {
		t.Fatalf("01:00–03:00 against a 20:00–02:00 day overlapped %s, want 1h", got)
	}

	// And a span that reaches forward into the next evening's window.
	from = time.Date(2026, 9, 3, 19, 0, 0, 0, time.UTC)
	to = time.Date(2026, 9, 3, 21, 0, 0, 0, time.UTC)
	if got := day.Overlap(from, to); got != time.Hour {
		t.Fatalf("19:00–21:00 overlapped %s, want 1h", got)
	}
}

func TestExposureIsAveragedOverTheItemNotSampledAtItsStart(t *testing.T) {
	plan := Plan{ListeningDay: &DaySpec{Start: "08:00", End: "23:00"}}
	day := ListeningDay{StartMinute: 8 * 60, EndMinute: 23 * 60}
	block := Block{ID: "general"}

	start := time.Date(2026, 9, 3, 7, 0, 0, 0, time.UTC)
	end := start.Add(2*time.Hour + 12*time.Minute)

	// The point sample is the old answer, and it is what left the episode owed.
	if got := plan.ExposureFor(block, start, day); got != 0 {
		t.Fatalf("sanity: the instant 07:00 should be outside the day, got %v", got)
	}

	got := plan.ExposureOver(block, start, end, day)
	want := 72.0 / 132.0
	if diff := got - want; diff > 0.001 || diff < -0.001 {
		t.Fatalf("an episode running 07:00–09:12 scored exposure %.3f, want %.3f — "+
			"seventy-two of its hundred and thirty-two minutes played to somebody awake", got, want)
	}
}

// A block that states its own exposure still wins outright. That rating is a
// statement about the block, not about the clock, and averaging it across a
// span would quietly reinterpret what its author wrote.
func TestAStatedBlockExposureIsNotAveraged(t *testing.T) {
	plan := Plan{ListeningDay: &DaySpec{Start: "08:00", End: "23:00"}}
	day := ListeningDay{StartMinute: 8 * 60, EndMinute: 23 * 60}
	half := 0.5
	block := Block{ID: "overnight", Exposure: &half}

	start := time.Date(2026, 9, 3, 7, 0, 0, 0, time.UTC)
	if got := plan.ExposureOver(block, start, start.Add(3*time.Hour), day); got != 0.5 {
		t.Fatalf("a block that says exposure 0.5 scored %v across a span", got)
	}
}

// An item with no length — a live stream, or a file nobody has probed — has no
// span to average over, and the instant is the best answer there is.
func TestAnItemWithNoLengthFallsBackToTheInstant(t *testing.T) {
	plan := Plan{ListeningDay: &DaySpec{Start: "08:00", End: "23:00"}}
	day := ListeningDay{StartMinute: 8 * 60, EndMinute: 23 * 60}
	block := Block{ID: "general"}

	inside := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	if got := plan.ExposureOver(block, inside, inside, day); got != 1 {
		t.Fatalf("a zero-length item inside the day scored %v, want 1", got)
	}
	outside := time.Date(2026, 9, 3, 3, 0, 0, 0, time.UTC)
	if got := plan.ExposureOver(block, outside, outside, day); got != 0 {
		t.Fatalf("a zero-length item outside the day scored %v, want 0", got)
	}
}

// End to end: the episode that runs into the morning is credited for the part
// the listener was awake for, rather than written off as reaching nobody.
func TestAnEpisodeRunningIntoTheMorningEarnsCredit(t *testing.T) {
	// 07:00, before an 08:00 listening day, with a 132-minute episode waiting.
	early := time.Date(2026, 9, 3, 7, 0, 0, 0, time.UTC)

	plan := Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 1}},
		ListeningDay: &DaySpec{Start: "08:00", End: "23:00"},
		Pools:        []Pool{{ID: "talk", SourceIDs: []string{"pod1"}}},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "talk", Weight: 1}},
		}},
	}

	s := newStation(t, plan, []Source{podcastSource("pod1", "The Show", "p1")},
		&stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
			"p1": {episode("long", "A long one", early.AddDate(0, 0, -30), 132)},
		}}, early)

	item, _ := s.decide()
	if item.ItemRef != "episode:long" {
		t.Fatalf("expected the only episode, got %s", item.ItemRef)
	}
	want := 72.0 / 132.0
	if diff := item.Exposure - want; diff > 0.01 || diff < -0.01 {
		t.Fatalf("an episode starting at 07:00 and running to 09:12 was stamped exposure %.3f, "+
			"want about %.3f — at 0 it airs to an audience and earns nothing", item.Exposure, want)
	}
}
