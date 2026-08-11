package channels

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// What removing the talk-run limit actually costs.
//
// The limit was the only thing capping item length in ordinary rotation, so
// with it gone there is nothing making a six-hour episode RARE. This measures
// how often one turns up across three days on a schedule shaped like the real
// station's — a long open gap in the middle of the day, booked shows either
// side.

// longFormStation is the real shape: a seven-hour hole between the morning slot
// and the afternoon one, several podcasts, and one show with enormous episodes.
func longFormStation(t *testing.T, start time.Time, withLimit bool) *Engine {
	t.Helper()

	sources := []Source{musicSource("mus1", "Easy Listening", "pl1")}
	episodes := map[string][]catalog.PodcastEpisode{}
	for show := 0; show < 6; show++ {
		id := "p" + strconv.Itoa(show)
		sources = append(sources, podcastSource("pod"+strconv.Itoa(show), "Show "+strconv.Itoa(show), id))
		list := []catalog.PodcastEpisode{}
		for index := 0; index < 20; index++ {
			list = append(list, episode(id+"-"+strconv.Itoa(index),
				"Show "+strconv.Itoa(show)+" ep "+strconv.Itoa(index),
				start.AddDate(0, 0, -20-index), 35+index%4*12))
		}
		episodes[id] = list
	}
	// The giant. Six episodes, four to six and a half hours each.
	huge := podcastSource("carlin", "Hardcore History", "phh")
	sources = append(sources, huge)
	list := []catalog.PodcastEpisode{}
	for index := 0; index < 6; index++ {
		list = append(list, episode("hh-"+strconv.Itoa(index),
			"Twilight of the Aesir "+strconv.Itoa(index),
			start.AddDate(0, 0, -200-index*30), 240+index*30))
	}
	episodes["phh"] = list

	songs := []catalog.MusicTrack{}
	for index := 0; index < 80; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%30), 200+index%6*15))
	}

	general := Block{
		ID: "general", Label: "General rotation", Default: true,
		Pools: []PoolRef{{Pool: "talk"}},
		Breaks: &BreakPolicy{
			Between:  []CategoryID{"talk"},
			Target:   BreakSize{Duration: "7m", Items: 2},
			Accept:   BreakRange{Duration: []string{"3m", "11m"}, Items: []int{1, 2}},
			Elements: []BreakElement{{Pool: "music", Count: []int{1, 2}, Fill: true}},
		},
	}
	if withLimit {
		general.Limits = BlockLimits{MaxUnbroken: []CategoryLimit{
			{Category: "talk", Max: "90m", ResetAfter: "15m", MinItem: "20m"},
		}}
	}

	plan := Plan{
		Version:    PlanVersion,
		Seed:       7,
		Categories: []CategoryDef{{ID: "talk", Target: 1}, {ID: "music", Target: 0}},
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
			{ID: "morning", SourceIDs: []string{"npr"}},
			{ID: "afternoon", SourceIDs: []string{"atc"}},
		},
		Blocks: []Block{
			general,
			{
				ID: "morning-slot", Label: "KRCC",
				Enter: BlockEntry{At: "08:00", Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "09:00"},
				Pools: []PoolRef{{Pool: "morning"}},
				Next:  "general",
			},
			{
				ID: "afternoon-slot", Label: "All Things Considered",
				Enter: BlockEntry{At: "16:00", Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "17:00"},
				Pools: []PoolRef{{Pool: "afternoon"}},
				Next:  "general",
			},
		},
	}
	npr := Source{ID: "npr", ChannelID: "c", Kind: SourceLiveStream, Label: "KRCC", Enabled: true,
		Role: RoleShow, Config: map[string]any{"url": "http://example.test/krcc"}}
	atc := Source{ID: "atc", ChannelID: "c", Kind: SourceLiveStream, Label: "ATC", Enabled: true,
		Role: RoleShow, Config: map[string]any{"url": "http://example.test/atc"}}
	sources = append(sources, npr, atc)

	if err := plan.Validate(); err != nil {
		t.Fatalf("plan: %v", err)
	}
	return &Engine{
		Plan: plan, Channel: Channel{ID: "lf"}, Sources: sources,
		History: NewMemoryHistory(), Obligations: NewMemoryObligations(),
		Catalog:  &stubCatalog{episodes: episodes, playlists: map[string][]catalog.MusicTrack{"pl1": songs}},
		Skips:    NewSkipRegistry(func() time.Time { return start }),
		Location: time.UTC,
	}
}

// How much of three days does the giant take, with the limit gone?
func TestLongFormFrequencyWithNoLimit(t *testing.T) {
	start := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	result, err := Simulate(context.Background(), longFormStation(t, start, false),
		SimOptions{Start: start, Duration: 72 * time.Hour})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}

	airings, minutes := 0, time.Duration(0)
	interrupted := 0
	for _, step := range result.Steps {
		if step.Item.SourceID != "carlin" {
			continue
		}
		airings++
		minutes += step.Length
		// The requirement: a long one must never be cut short by a booked show.
		if step.Item.DurationSeconds > 0 &&
			step.Length < time.Duration(step.Item.DurationSeconds)*time.Second-time.Minute {
			interrupted++
		}
	}
	total := time.Duration(0)
	for _, step := range result.Steps {
		total += step.Length
	}
	share := 0.0
	if total > 0 {
		share = float64(minutes) / float64(total)
	}
	t.Logf("Hardcore History over 72h: %d airings, %s of %s (%.0f%% of the station), %d cut short",
		airings, minutes.Round(time.Minute), total.Round(time.Minute), share*100, interrupted)

	if interrupted > 0 {
		t.Fatalf("%d long episodes were cut short by booked programming — that must never happen", interrupted)
	}
	// This is the assertion that documents the hole. "Not often" is not a thing
	// the engine currently believes.
	if share > 0.20 {
		t.Fatalf("long-form took %.0f%% of three days with nothing making it rare:\n%s",
			share*100, result.Format(false))
	}
}

// Over three weeks, how often does the giant actually turn up? "Sometimes, but
// not often" needs to be a measurable number, not a hope.
func TestLongFormTurnsUpOccasionally(t *testing.T) {
	start := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	result, err := Simulate(context.Background(), longFormStation(t, start, false),
		SimOptions{Start: start, Duration: 21 * 24 * time.Hour, MaxSteps: 40000})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	airings, minutes, cut := 0, time.Duration(0), 0
	for _, step := range result.Steps {
		if step.Item.SourceID != "carlin" {
			continue
		}
		airings++
		minutes += step.Length
		if step.Item.DurationSeconds > 0 &&
			step.Length < time.Duration(step.Item.DurationSeconds)*time.Second-time.Minute {
			cut++
		}
	}
	t.Logf("over 21 days: %d airings, %s total, %d cut short", airings, minutes.Round(time.Minute), cut)
	if cut > 0 {
		t.Fatalf("%d giants were cut short by booked programming", cut)
	}
	if airings == 0 {
		t.Fatalf("never played in three weeks — that is a ban, not 'sometimes'")
	}
	if airings > 6 {
		t.Fatalf("%d airings in three weeks is not 'not often'", airings)
	}
}
