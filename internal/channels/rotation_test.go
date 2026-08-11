package channels

import (
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The parts of the old rotation that survived the rebuild: repeat limits, the
// listening day, and how episodes are ordered. Everything that used to be here
// about talk and music now lives in a plan, and is tested through the engine.

func TestMaxAiringsScalesWithDuration(t *testing.T) {
	cases := []struct {
		seconds int
		want    int
	}{
		{0, 1},           // unknown length: once
		{25 * 60, 3},     // a short show: catch it morning, afternoon, evening
		{60 * 60, 2},     // an hour: twice
		{3 * 60 * 60, 1}, // three hours: once, or it is most of your day
		{8 * 60 * 60, 1}, // too long for the budget still gets one airing
	}
	for _, tc := range cases {
		if got := maxAiringsPerDay(tc.seconds); got != tc.want {
			t.Fatalf("maxAiringsPerDay(%ds) = %d, want %d", tc.seconds, got, tc.want)
		}
	}
}

// The airing cap counts; item separation decides how soon. Keeping them apart
// is what lets the "how soon" half adapt to a station whose whole library is
// three tracks, where any fixed gap can only ever be broken.
func TestTheAiringCapCountsAndNothingElse(t *testing.T) {
	length := 25 * 60 // three airings allowed

	if !mayAirAgain(length, 0) {
		t.Fatalf("something that has never aired must be allowed")
	}
	if !mayAirAgain(length, 1) {
		t.Fatalf("one airing of a 25-minute item leaves two")
	}
	if mayAirAgain(length, 3) {
		t.Fatalf("three airings is the cap for a 25-minute item")
	}
	if mayAirAgain(3*60*60, 1) {
		t.Fatalf("a three-hour item gets one airing a day")
	}
}

func TestListeningDayCanCrossMidnight(t *testing.T) {
	day := ListeningDay{StartMinute: 20 * 60, EndMinute: 2 * 60} // 20:00–02:00
	inside := []int{20, 22, 23, 0, 1}
	outside := []int{2, 8, 12, 19}
	for _, hour := range inside {
		at := time.Date(2026, 8, 10, hour, 30, 0, 0, time.UTC)
		if !day.Contains(at) {
			t.Fatalf("%02d:30 should be inside a 20:00–02:00 listening day", hour)
		}
	}
	for _, hour := range outside {
		at := time.Date(2026, 8, 10, hour, 30, 0, 0, time.UTC)
		if day.Contains(at) {
			t.Fatalf("%02d:30 should be outside a 20:00–02:00 listening day", hour)
		}
	}
}

// The listening day's start is built as a wall clock, not as midnight plus a
// duration — on the day the clocks change those are an hour apart, and a
// station that holds every overnight release an hour too long twice a year is
// impossible to debug from the listening end.
func TestListeningDayNextStartSurvivesADSTChange(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("no tzdata for America/Denver: %v", err)
	}
	day := ListeningDay{StartMinute: 8 * 60, EndMinute: 23 * 60}
	// 2026-03-08 is the US spring-forward day: 02:00 becomes 03:00.
	night := time.Date(2026, 3, 8, 1, 0, 0, 0, denver)
	start := day.NextStart(night)
	if start.Hour() != 8 || start.Minute() != 0 {
		t.Fatalf("listening day started at %s on a spring-forward day, want 08:00", start.Format("15:04 MST"))
	}
	if start.Day() != 8 {
		t.Fatalf("listening day jumped to %s", start.Format("2006-01-02"))
	}
}

func TestSourceCategoryReadsConfigThenRole(t *testing.T) {
	music := Source{Kind: SourceMusicPlaylist, Role: RoleMusic}
	if got := SourceCategory(music); got != LegacyCategoryMusic {
		t.Fatalf("a music-role source without a category should fall back to %q, got %q", LegacyCategoryMusic, got)
	}
	talk := Source{Kind: SourcePodcastSubscription, Role: RoleTalk}
	if got := SourceCategory(talk); got != LegacyCategoryTalk {
		t.Fatalf("a talk-role source should fall back to %q, got %q", LegacyCategoryTalk, got)
	}
	// A station that has said what this IS wins over the legacy role entirely.
	comedy := Source{Kind: SourcePodcastSubscription, Role: RoleTalk, Config: map[string]any{"category": "comedy"}}
	if got := SourceCategory(comedy); got != "comedy" {
		t.Fatalf("configured category should win, got %q", got)
	}
}

func TestTraitsComeFromTheKindAndCanBeOverridden(t *testing.T) {
	pod := TraitsFor(Source{Kind: SourcePodcastSubscription, Role: RoleTalk})
	if !pod.SupportsFreshness || !pod.HasCreator || !pod.SharedCreator {
		t.Fatalf("a podcast has dated episodes, a host, and one host for all of them: %+v", pod)
	}
	playlist := TraitsFor(Source{Kind: SourceMusicPlaylist, Role: RoleMusic})
	if !playlist.HasCreator {
		t.Fatalf("a playlist's tracks have artists")
	}
	if playlist.SharedCreator {
		t.Fatalf("a playlist is many artists behind one row — separating the SOURCE would ban music sets")
	}
	station := TraitsFor(Source{Kind: SourceInternetStation, Role: RoleTalk})
	if !station.Continuous {
		t.Fatalf("a stream never ends, so something has to bound it")
	}
	spots := TraitsFor(Source{Kind: SourceFilePool, Role: RoleCommercial})
	if !spots.Interstitial {
		t.Fatalf("a commercial pool is separator inventory")
	}
	// A folder could be jingles or oldies, and nothing about "file-pool"
	// reveals which — which is exactly why it is overridable.
	oldies := TraitsFor(Source{
		Kind: SourceFilePool, Role: RoleMusic,
		Config: map[string]any{"traits": map[string]any{"hasCreator": true}},
	})
	if !oldies.HasCreator {
		t.Fatalf("the override was ignored: %+v", oldies)
	}
}

func TestNewestFirstOrdersByPublicationNotFeedOrder(t *testing.T) {
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	// Feed order for anything ingested chronologically is OLDEST first.
	items := []catalog.PodcastEpisode{
		{ID: "old", PublishedAt: &old},
		{ID: "mid", PublishedAt: &mid},
		{ID: "recent", PublishedAt: &recent},
	}
	sorted := newestFirst(items)
	if sorted[0].ID != "recent" || sorted[2].ID != "old" {
		t.Fatalf("newestFirst returned %s, %s, %s", sorted[0].ID, sorted[1].ID, sorted[2].ID)
	}
}

func TestNewestFirstSinksUndatedEpisodes(t *testing.T) {
	dated := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	items := []catalog.PodcastEpisode{
		{ID: "undated"},
		{ID: "dated", PublishedAt: &dated},
	}
	sorted := newestFirst(items)
	if sorted[0].ID != "dated" {
		t.Fatalf("an undated episode jumped the queue on a nil comparison")
	}
}

func TestEpisodeProgressListened(t *testing.T) {
	cases := []struct {
		name     string
		progress EpisodeProgress
		duration int
		want     bool
	}{
		{"never started", EpisodeProgress{}, 1800, false},
		{"explicitly complete", EpisodeProgress{Completed: true}, 1800, true},
		{"most of the way through", EpisodeProgress{ProgressSeconds: 1700}, 1800, true},
		{"barely started", EpisodeProgress{ProgressSeconds: 30}, 1800, false},
		// The signal that actually works: feed episodes routinely have no
		// duration, so a ratio can never fire for them.
		{"two minutes in, no duration known", EpisodeProgress{ProgressSeconds: 130}, 0, true},
		{"one minute in, no duration known", EpisodeProgress{ProgressSeconds: 60}, 0, false},
	}
	for _, tc := range cases {
		if got := tc.progress.listened(tc.duration); got != tc.want {
			t.Fatalf("%s: listened = %v, want %v", tc.name, got, tc.want)
		}
	}
}
