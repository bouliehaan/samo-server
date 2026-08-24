package channels

import (
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The morning of the "one day is infinite" report: at about 09:30 the station
// refused a podcast because it had "already played it today", and put out
// something like forty minutes of music instead.
//
// The airing cap counts, and item separation waits — rotation.go says so in as
// many words, and warns that two rules both saying "not yet" with different
// numbers is how they drift apart. The cap was reading a ROLLING twenty-four
// hours, which has no boundary to roll over: at 09:30 it still reached back to
// 09:30 yesterday, so last night's airing was charged to this morning's
// allowance and a once-a-day episode could not go out until the same hour came
// round again. That is the cap quietly becoming a second, longer "not yet".
//
// These two tests pin both halves of the correction: yesterday's airing stops
// counting, and today's still does.

// cappedStation is a station with one long-enough-to-be-rationed episode and
// enough music to fall back on, which is what the report described happening.
func cappedStation(t *testing.T, now time.Time) *station {
	t.Helper()

	plan := Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		ListeningDay: &DaySpec{Start: "08:00", End: "23:00"},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"pod1"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
		},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "talk", Weight: 1}, {Pool: "music", Weight: 1}},
		}},
	}

	songs := []catalog.MusicTrack{}
	for index := 0; index < 20; index++ {
		songs = append(songs, track(
			"t"+string(rune('a'+index)), "Song", "Artist "+string(rune('a'+index)), 210))
	}

	// Seventy minutes, so maxAiringsPerDay is int(2h/70m) = 1: exactly the
	// once-a-day shape the report was about. Published long ago, so it is back
	// catalogue rather than something the station OWES — an owed item reads its
	// exposure credit instead of raw history and would not exercise the cap.
	old := now.AddDate(0, 0, -40)

	return newStation(t, plan,
		[]Source{podcastSource("pod1", "The Show", "p1"), musicSource("mus1", "House", "pl1")},
		&stubCatalog{
			episodes:  map[string][]catalog.PodcastEpisode{"p1": {episode("ep1", "An episode", old, 70)}},
			playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		}, now)
}

// airingCapRejection is what the decision said the cap did to a ref, or "" if
// the cap had nothing to say about it.
func airingCapRejection(decision Decision, ref string) string {
	for _, rejection := range decision.Rejected {
		if rejection.Ref == ref && rejection.Rule == "airingCap" {
			return rejection.Reason
		}
	}
	return ""
}

// THE COMPLAINT, as reported. An episode that aired at 20:00 last night — a
// different listening day, long over — must not be "already played today" at
// 09:30, and the station must come back to spoken word rather than reaching for
// music because the only talk it had was wrongly held.
func TestLastNightsAiringDoesNotSpendTodaysAllowance(t *testing.T) {
	morning := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)
	s := cappedStation(t, morning)

	// Yesterday evening, inside yesterday's listening day. A rolling 24-hour
	// window still contains this; the listening day does not.
	lastNight := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	s.history.Record(MemoryPlay{
		SourceID: "pod1", ItemRef: "episode:ep1", Category: "talk",
		StartedAt: lastNight, EndedAt: lastNight.Add(70 * time.Minute),
		DurationSeconds: 70 * 60,
	})

	item, decision := s.decide()

	if reason := airingCapRejection(decision, "episode:ep1"); reason != "" {
		t.Fatalf("at 09:30 the cap still refused an episode that aired at 20:00 the previous "+
			"listening day: %q — the cap is reading a rolling window, not the day", reason)
	}
	if item.ItemRef != "episode:ep1" {
		t.Fatalf("the station played %s (%s) instead of the episode it was free to air; rejections: %s",
			item.ItemRef, item.Category, rejectionsFor(decision, "pod1"))
	}
}

// The other half: the cap must still do its job WITHIN the day. Narrowing the
// window must not switch the rule off.
//
// Measured at 18:00 rather than mid-morning on purpose. A repeat an hour later
// is refused by item SEPARATION, which is a different rule with a different
// number, and a test that let separation answer would pass whether the cap
// worked or not. By 18:00 separation has long since released it and the cap is
// the only thing left standing between the listener and a second airing.
func TestThisMorningsAiringStillSpendsTodaysAllowance(t *testing.T) {
	evening := time.Date(2026, 8, 11, 18, 0, 0, 0, time.UTC)
	s := cappedStation(t, evening)

	earlier := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	s.history.Record(MemoryPlay{
		SourceID: "pod1", ItemRef: "episode:ep1", Category: "talk",
		StartedAt: earlier, EndedAt: earlier.Add(70 * time.Minute),
		DurationSeconds: 70 * 60,
	})

	_, decision := s.decide()

	if airingCapRejection(decision, "episode:ep1") == "" {
		t.Fatalf("an episode already heard once this morning was not held by the airing cap; "+
			"rejections: %s", rejectionsFor(decision, "pod1"))
	}
}

// The small hours belong to the day that just ended, exactly as listeningDayKey
// has it. Without that agreement, an episode aired at 22:00 would come free
// again the moment the clock passed midnight.
func TestTheListeningDayStartAgreesWithItsKey(t *testing.T) {
	day := ListeningDay{StartMinute: 8 * 60, EndMinute: 23 * 60}

	cases := []struct {
		at    time.Time
		start time.Time
		name  string
	}{
		{
			name:  "mid-morning belongs to today",
			at:    time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC),
			start: time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC),
		},
		{
			name:  "the small hours still belong to yesterday",
			at:    time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC),
			start: time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC),
		},
		{
			name:  "the instant the day opens is already today",
			at:    time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC),
			start: time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := listeningDayStart(day, time.UTC, testCase.at)
			if !got.Equal(testCase.start) {
				t.Fatalf("listeningDayStart(%s) = %s, want %s",
					testCase.at.Format(time.RFC3339), got.Format(time.RFC3339),
					testCase.start.Format(time.RFC3339))
			}
			// It must name the same day the block cap keys on, or the two
			// "per day" rules are counting different days.
			if key := listeningDayKey(day, time.UTC, testCase.at); key != got.Format("2006-01-02") {
				t.Fatalf("listeningDayStart says %s but listeningDayKey says %s",
					got.Format("2006-01-02"), key)
			}
			// Never zero, or the store reads it as "no window" and falls back
			// to twenty-four hours — readmitting the day we just excluded.
			if elapsed := listeningDayElapsed(day, time.UTC, testCase.at); elapsed <= 0 {
				t.Fatalf("listeningDayElapsed returned %s", elapsed)
			}
		})
	}
}

// The same bug, reached through the other door: an episode the station still
// OWES you.
//
// The airing cap reads exposure credit instead of raw history for owed items,
// so that an airing nobody heard cannot burn the episode. But credit accumulates
// over an obligation's whole life, which is days — so once the day count was
// correct, substituting credit put yesterday's airing back into today's
// allowance. On anything long enough to earn one airing a day that is a
// permanent ban, and an episode owed a SECOND surfacing could never get it.
//
// Credit may lower the count. It may not raise it.
func TestAnOwedEpisodeAiredYesterdayIsNotBannedToday(t *testing.T) {
	lastEvening := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)

	plan := Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		ListeningDay: &DaySpec{Start: "08:00", End: "23:00"},
		// S-tier wants two surfacings, so one full airing leaves it owed.
		Freshness: FreshnessPolicy{Surfacings: map[string]int{"S": 2}},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"pod1"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
		},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "talk", Weight: 1}, {Pool: "music", Weight: 1}},
		}},
	}

	show := podcastSource("pod1", "The Show", "p1")
	show.Config["tier"] = "S"

	// Music has to be here, and not as scenery. With talk as the only pool the
	// relaxation ladder gives the cap up the moment nothing survives it, and the
	// episode plays whether the rule was right or wrong. Music surviving is what
	// keeps the cap's verdict standing — and is exactly what the station reached
	// for when it locked the talk out.
	songs := []catalog.MusicTrack{}
	for index := 0; index < 20; index++ {
		songs = append(songs, track(
			"m"+string(rune('a'+index)), "Song", "Artist "+string(rune('a'+index)), 210))
	}

	s := newStation(t, plan, []Source{show, musicSource("mus1", "House", "pl1")},
		&stubCatalog{
			episodes: map[string][]catalog.PodcastEpisode{
				"p1": {episode("ep1", "A new episode", lastEvening.Add(-6*time.Hour), 70)},
			},
			playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		}, lastEvening)

	// Airs once last evening, in the clear — real exposure, real credit.
	if aired := s.play(); aired.ItemRef != "episode:ep1" {
		t.Fatalf("setup: expected the episode to air last evening, got %q", aired.ItemRef)
	}

	// Next morning.
	s.now = time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)

	_, decision := s.decide()

	if reason := airingCapRejection(decision, "episode:ep1"); reason != "" {
		t.Fatalf("an episode the station still owes was refused this morning for last night's "+
			"airing: %q", reason)
	}
}
