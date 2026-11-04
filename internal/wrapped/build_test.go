package wrapped

import (
	"testing"
	"time"
)

func at(day, hour int) time.Time {
	return time.Date(2026, time.January, 1, hour, 0, 0, 0, time.UTC).AddDate(0, 0, day-1)
}

func listen(artist, title string, when time.Time, seconds int) Listen {
	return Listen{Artist: artist, Album: artist + " LP", Title: title, At: when, Seconds: seconds}
}

func TestBuildMusicRanksByPlaysAndKeepsTheShapeOfTheYear(t *testing.T) {
	listens := []Listen{
		listen("Aurora Fields", "Night Drive", at(1, 9), 200),
		listen("aurora fields", "night  drive", at(1, 10), 200), // same track, other spelling
		listen("Aurora Fields", "Low Tide", at(2, 23), 180),
		listen("Delta Static", "Frequencies", at(3, 23), 240),
		listen("Delta Static", "Frequencies", at(40, 23), 240),
		listen("Hana Sato", "Kaze", at(40, 1), 0), // length unknown: estimated
	}
	stats := BuildMusic(listens, time.UTC)
	if stats.Listens != 6 || stats.Artists != 3 || stats.Tracks != 4 {
		t.Fatalf("counts = %d listens, %d artists, %d tracks", stats.Listens, stats.Artists, stats.Tracks)
	}
	if got := stats.TopArtists[0]; got.Name != "Aurora Fields" || got.Plays != 3 || got.Minutes != 9 {
		t.Fatalf("top artist = %+v", got)
	}
	// Two plays each: the tie goes to minutes heard, so the longer Frequencies
	// ranks above Night Drive.
	if stats.TopTracks[0].Name != "Frequencies" || stats.TopTracks[1].Name != "Night Drive" {
		t.Fatalf("top tracks = %+v", stats.TopTracks[:2])
	}
	if stats.Minutes != (200+200+180+240+240+estimatedTrackSeconds)/60 {
		t.Fatalf("minutes = %d", stats.Minutes)
	}
	if stats.MinutesEstimated {
		t.Fatal("one listen in six without a length is not an estimate")
	}
	if stats.Months[0] != (200+200+180+240)/60 || stats.Months[1] != (240+estimatedTrackSeconds)/60 {
		t.Fatalf("months = %v", stats.Months[:2])
	}
	if stats.TopMonth != 1 {
		t.Fatalf("top month = %d", stats.TopMonth)
	}
	if stats.LongestStreak != 3 || stats.Days != 4 {
		t.Fatalf("streak %d over %d days", stats.LongestStreak, stats.Days)
	}
	if stats.BusiestDay != (DayCount{Date: "2026-01-01", Listens: 2}) {
		t.Fatalf("busiest day = %+v", stats.BusiestDay)
	}
	if stats.Clock.Name != "Night owl" || stats.Clock.PeakHour != 23 {
		t.Fatalf("clock = %+v", stats.Clock)
	}
	if stats.First.Title != "Night Drive" || !stats.First.At.Equal(at(1, 9)) {
		t.Fatalf("first = %+v", stats.First)
	}
	if stats.OnRepeat.Plays != 0 {
		t.Fatalf("twice in a day is not on repeat: %+v", stats.OnRepeat)
	}
}

func TestBuildMusicFindsTheTrackOnRepeat(t *testing.T) {
	var listens []Listen
	for i := 0; i < 5; i++ {
		listens = append(listens, listen("Miles Okonkwo", "Blue Hours", at(100, 12).Add(time.Duration(i)*5*time.Minute), 290))
	}
	listens = append(listens, listen("Miles Okonkwo", "Other", at(101, 12), 290))
	stats := BuildMusic(listens, time.UTC)
	if stats.OnRepeat.Title != "Blue Hours" || stats.OnRepeat.Plays != 5 || stats.OnRepeat.Date != "2026-04-10" {
		t.Fatalf("on repeat = %+v", stats.OnRepeat)
	}
}

func TestBuildMusicReadsDaysInTheServersZone(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skip("no zone data")
	}
	// 03:00 UTC on Jan 2 is the evening of Jan 1 in Denver.
	stats := BuildMusic([]Listen{listen("A", "B", time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC), 200)}, denver)
	if stats.BusiestDay.Date != "2026-01-01" || stats.Clock.PeakHour != 20 {
		t.Fatalf("day %s hour %d", stats.BusiestDay.Date, stats.Clock.PeakHour)
	}
}

func TestBuildMusicCallsMostlyUnknownLengthsAnEstimate(t *testing.T) {
	stats := BuildMusic([]Listen{listen("A", "B", at(1, 1), 0), listen("A", "C", at(1, 2), 0)}, time.UTC)
	if !stats.MinutesEstimated || stats.Minutes != 2*estimatedTrackSeconds/60 {
		t.Fatalf("minutes %d estimated %v", stats.Minutes, stats.MinutesEstimated)
	}
	if BuildMusic(nil, time.UTC) != nil {
		t.Fatal("no listens, no music")
	}
}

func TestTierIsJudgedPerDayCovered(t *testing.T) {
	cases := []struct {
		minutes, days int
		want          string
	}{
		{90 * 365, 365, "Baller"},
		{30 * 365, 365, "Shot caller"},
		{29 * 365, 365, "Not-at-aller"},
		// A history switched on in December is not judged against a year.
		{95 * 20, 20, "Baller"},
		{0, 0, "Not-at-aller"},
	}
	for _, c := range cases {
		if got := TierFor(c.minutes, c.days).Name; got != c.want {
			t.Errorf("TierFor(%d, %d) = %s, want %s", c.minutes, c.days, got, c.want)
		}
	}
}
