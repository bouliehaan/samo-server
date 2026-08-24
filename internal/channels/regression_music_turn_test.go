package channels

import (
	"strconv"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The shuffle bag is measured in the wrong units.
//
// "I have 300+ songs, I play one or two between talk segments, and I heard
// Saturday by Elton John five times in a few days."
//
// turnsForShuffledSources returns the playlist's total RUNNING TIME and
// itemSeparation applies it as a WALL-CLOCK window. Those are the same number
// only on a station that plays nothing but that playlist — which is exactly
// what every existing rotation test is (musicOnlyPlan). Where music is filler
// between talk, seventeen hours of wall clock contains two hours of music, so
// the whole playlist becomes eligible again after forty songs have aired and
// the other three hundred are still waiting.

// fillerMusicPlan is the real station's shape: talk is the programming, music
// is what plays between it. Music targets ZERO airtime, exactly as
// testdata/jake-channel-plan.json does.
func fillerMusicPlan() Plan {
	return Plan{
		Version: PlanVersion,
		Categories: []CategoryDef{
			{ID: "talk", Label: "Talk", Target: 1},
			{ID: "music", Label: "Music", Target: 0},
		},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"pod0", "pod1", "pod2"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
		},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "talk", Weight: 1}, {Pool: "music", Weight: 1}},
			Limits: BlockLimits{
				// One or two songs between talk segments, and talk never runs
				// more than one episode unbroken. This is what makes music a
				// minority of airtime rather than the whole station.
				MaxUnbroken: []CategoryLimit{
					{Category: "talk", Max: "45m", ResetAfter: "1m"},
					{Category: "music", Max: "8m", ResetAfter: "1m"},
				},
			},
		}},
	}
}

// TestAShuffledPlaylistTurnsOverInMusicNotWallClock is the regression.
func TestAShuffledPlaylistTurnsOverInMusicNotWallClock(t *testing.T) {
	start := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)

	// 320 songs, ~3.5 minutes each: a real playlist, well over the 300 the
	// complaint is about.
	songs := make([]catalog.MusicTrack, 0, 320)
	for i := 0; i < 320; i++ {
		songs = append(songs, track("t"+strconv.Itoa(i), "Song "+strconv.Itoa(i),
			"Artist "+strconv.Itoa(i%150), 190+(i%7)*15))
	}

	// Three talk shows with deep back catalogues, so talk never runs dry and
	// music stays the filler it is on the real station.
	sources := []Source{musicSource("mus1", "House Playlist", "pl1")}
	episodes := map[string][]catalog.PodcastEpisode{}
	for show := 0; show < 3; show++ {
		id := "p" + strconv.Itoa(show)
		sources = append(sources, podcastSource("pod"+strconv.Itoa(show), "Show "+strconv.Itoa(show), id))
		list := []catalog.PodcastEpisode{}
		for ep := 0; ep < 120; ep++ {
			list = append(list, episode(id+"-"+strconv.Itoa(ep),
				"Show "+strconv.Itoa(show)+" ep "+strconv.Itoa(ep),
				start.AddDate(0, 0, -ep-1), 35+ep%3*10))
		}
		episodes[id] = list
	}

	s := newStation(t, fillerMusicPlan(), sources,
		&stubCatalog{playlists: map[string][]catalog.MusicTrack{"pl1": songs}, episodes: episodes},
		start)

	// Four days of radio — "a few days".
	byTrack := map[string]int{}
	deadline := start.AddDate(0, 0, 4)
	for s.now.Before(deadline) {
		item := s.play()
		if item.Category == "music" && item.ItemRef != "" {
			byTrack[item.ItemRef]++
		}
	}

	worst, worstRef := 0, ""
	for ref, plays := range byTrack {
		if plays > worst {
			worst, worstRef = plays, ref
		}
	}
	aired := 0
	for range byTrack {
		aired++
	}
	t.Logf("over 4 days: %d distinct songs aired of 320, most-played %s aired %d times",
		aired, worstRef, worst)

	// Nothing should come round twice while hundreds of songs have never aired
	// at all. This is the whole claim a shuffle bag makes.
	if worst > 1 && aired < 320 {
		t.Fatalf("a song aired %d times while only %d of 320 songs ever played — "+
			"the playlist repeats before it has turned over", worst, aired)
	}
}

// The bag has to keep turning, not seize up.
//
// A rule that counts songs instead of minutes has an obvious failure mode the
// wall-clock version did not: if nothing is ever eligible the station stops
// playing music altogether. It cannot happen — the tail is held out of the turn
// precisely so there is always something to draw — but "cannot happen" is worth
// a test, because the symptom (music quietly disappearing) is one nobody would
// attribute to the shuffle.
func TestTheShuffleBagKeepsTurningOverWeeks(t *testing.T) {
	start := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)

	songs := make([]catalog.MusicTrack, 0, 320)
	for i := 0; i < 320; i++ {
		songs = append(songs, track("t"+strconv.Itoa(i), "Song "+strconv.Itoa(i),
			"Artist "+strconv.Itoa(i%150), 190+(i%7)*15))
	}
	sources := []Source{musicSource("mus1", "House Playlist", "pl1")}
	episodes := map[string][]catalog.PodcastEpisode{}
	for show := 0; show < 3; show++ {
		id := "p" + strconv.Itoa(show)
		sources = append(sources, podcastSource("pod"+strconv.Itoa(show), "Show "+strconv.Itoa(show), id))
		list := []catalog.PodcastEpisode{}
		for ep := 0; ep < 400; ep++ {
			list = append(list, episode(id+"-"+strconv.Itoa(ep),
				"Show "+strconv.Itoa(show)+" ep "+strconv.Itoa(ep),
				start.AddDate(0, 0, -ep-1), 35+ep%3*10))
		}
		episodes[id] = list
	}

	s := newStation(t, fillerMusicPlan(), sources,
		&stubCatalog{playlists: map[string][]catalog.MusicTrack{"pl1": songs}, episodes: episodes},
		start)

	byTrack := map[string]int{}
	musicInLastWeek := 0
	deadline := start.AddDate(0, 0, 28)
	weekFour := start.AddDate(0, 0, 21)
	for s.now.Before(deadline) {
		at := s.now
		item := s.play()
		if item.Category != "music" || item.ItemRef == "" {
			continue
		}
		byTrack[item.ItemRef]++
		if !at.Before(weekFour) {
			musicInLastWeek++
		}
	}

	// Music must still be playing in week four, not starved by its own rule.
	if musicInLastWeek == 0 {
		t.Fatal("no music aired in the fourth week — the shuffle bag seized up")
	}

	most, fewest := 0, 1<<30
	for _, plays := range byTrack {
		if plays > most {
			most = plays
		}
		if plays < fewest {
			fewest = plays
		}
	}
	t.Logf("28 days: %d of 320 songs aired, %d plays in week four, most-played %d, least-played %d",
		len(byTrack), musicInLastWeek, most, fewest)

	// Over four weeks nothing should have run away from the field. The tail
	// held in hand allows a little slack; a factor of two is not slack, it is
	// the old bug coming back.
	if len(byTrack) > 0 && most > fewest+1 && most > 2*fewest {
		t.Fatalf("play counts spread from %d to %d — the rotation is not even", fewest, most)
	}
}
