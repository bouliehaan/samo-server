package channels

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// A talk station: podcasts back to back, a song or two between them, and no
// percentage chasing music into twenty-minute blocks.
//
// The station this engine kept producing was the other thing — music has a 25%
// target, talk is permanently ahead of its own, so music carries a permanent
// deficit and wins repeatedly, and a minimum run turns each win into twenty
// minutes. On a channel with eighteen podcasts that is a machine for
// interrupting you.
//
// The fix is not a new mechanism. It is using the RIGHT one: music is a
// separator (a break policy), not a quota (a category target). Setting the
// music target to zero stops the balance reaching for it, and the break policy
// puts one or two songs between spoken items. This test is what "a song or two
// between podcasts" has to look like from the outside.
func talkStationPlan() Plan {
	return Plan{
		Version: PlanVersion,
		Categories: []CategoryDef{
			{ID: "talk", Label: "Talk", Target: 1},
			// Zero: music never wins on balance. It appears only where
			// something explicitly asks for it — which is the break below.
			{ID: "music", Label: "Music", Target: 0},
		},
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
		},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			// The rotation plays spoken word. Music is not in it.
			Pools: []PoolRef{{Pool: "talk"}},
			Breaks: &BreakPolicy{
				Between:  []CategoryID{"talk"},
				Target:   BreakSize{Duration: "7m", Items: 2},
				Accept:   BreakRange{Duration: []string{"3m", "11m"}, Items: []int{1, 2}},
				Elements: []BreakElement{{Pool: "music", Count: []int{1, 2}, Fill: true}},
			},
			// No maxUnbroken: the breaks do the separating now, so a limit that
			// forces a long music block on top of them is the thing being
			// complained about.
		}},
	}
}

func TestATalkStationPlaysPodcastSongSongPodcast(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)

	sources := []Source{musicSource("mus1", "Easy Listening", "pl1")}
	episodes := map[string][]catalog.PodcastEpisode{}
	for show := 0; show < 6; show++ {
		id := "p" + strconv.Itoa(show)
		src := podcastSource("pod"+strconv.Itoa(show), "Show "+strconv.Itoa(show), id)
		sources = append(sources, src)
		list := []catalog.PodcastEpisode{}
		for index := 0; index < 12; index++ {
			list = append(list, episode(
				id+"-"+strconv.Itoa(index), "Show "+strconv.Itoa(show)+" ep "+strconv.Itoa(index),
				now.AddDate(0, 0, -30-index), 35+index%4*10))
		}
		episodes[id] = list
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 40; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%20), 200+index%5*20))
	}

	s := newStation(t, talkStationPlan(), sources,
		&stubCatalog{episodes: episodes, playlists: map[string][]catalog.MusicTrack{"pl1": songs}}, now)

	order := []CategoryID{}
	aired := map[CategoryID]time.Duration{}
	for i := 0; i < 24; i++ {
		before := s.now
		item := s.play()
		order = append(order, item.Category)
		aired[item.Category] += s.now.Sub(before)
	}

	// The shape: never more than two songs together, and never a long block of
	// them. That is the whole complaint.
	longestMusic, run := 0, 0
	for _, category := range order {
		if category == "music" {
			run++
			if run > longestMusic {
				longestMusic = run
			}
			continue
		}
		run = 0
	}
	if longestMusic > 2 {
		t.Fatalf("%d songs in a row — a break is a song or two, not a music block: %v", longestMusic, order)
	}

	// Measured in AIRTIME, not items. Two songs after a forty-five minute
	// podcast is two items and seven minutes; counting items would call that a
	// fifty-fifty station, which is not what anybody hears.
	if aired["music"] == 0 {
		t.Fatalf("no music at all — the breaks are not firing: %v", order)
	}
	share := float64(aired["music"]) / float64(aired["talk"]+aired["music"])
	if share > 0.25 {
		t.Fatalf("music took %.0f%% of the airtime on a station that asked for none by target: %v",
			share*100, order)
	}
}

// The same station over three days: heavy talk is FINE, and music must not
// creep up as the balance tries to catch up, because it is not trying to.
func TestATalkStationStaysTalkHeavyOverDays(t *testing.T) {
	start := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)

	sources := []Source{musicSource("mus1", "Easy Listening", "pl1")}
	episodes := map[string][]catalog.PodcastEpisode{}
	for show := 0; show < 8; show++ {
		id := "p" + strconv.Itoa(show)
		sources = append(sources, podcastSource("pod"+strconv.Itoa(show), "Show "+strconv.Itoa(show), id))
		list := []catalog.PodcastEpisode{}
		for index := 0; index < 25; index++ {
			list = append(list, episode(
				id+"-"+strconv.Itoa(index), "Show "+strconv.Itoa(show)+" ep "+strconv.Itoa(index),
				start.AddDate(0, 0, -20-index), 30+index%5*15))
		}
		episodes[id] = list
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 120; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%40), 190+index%7*15))
	}

	engine := &Engine{
		Plan:        talkStationPlan(),
		Channel:     Channel{ID: "talk-sim"},
		Sources:     sources,
		History:     NewMemoryHistory(),
		Obligations: NewMemoryObligations(),
		Catalog:     &stubCatalog{episodes: episodes, playlists: map[string][]catalog.MusicTrack{"pl1": songs}},
		Skips:       NewSkipRegistry(func() time.Time { return start }),
		Location:    time.UTC,
	}
	result, err := Simulate(context.Background(), engine, SimOptions{Start: start, Duration: 72 * time.Hour})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	t.Log("\n" + result.Format(false))

	shares := map[CategoryID]int{}
	for _, category := range result.Report.Categories {
		shares[category.Category] = category.Percent
	}
	if shares["music"] > 25 {
		t.Fatalf("music crept to %d%% on a station that never asked for any: %s",
			shares["music"], result.Format(false))
	}
	if shares["talk"] < 70 {
		t.Fatalf("talk only reached %d%% on a talk station", shares["talk"])
	}
	for _, run := range result.Report.LongestRun {
		if run.Name == "music" && run.Minutes > 15 {
			t.Fatalf("longest music run was %dm — that is a music block, not a break", run.Minutes)
		}
	}
	if result.Report.Gaps > 0 {
		t.Fatalf("%d moments with nothing to play", result.Report.Gaps)
	}
}
