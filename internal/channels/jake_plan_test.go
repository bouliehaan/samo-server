package channels

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Drives the ACTUAL plan document destined for Jake Channel against a station
// shaped like the real one, so the shape is proven before it goes live.
func TestJakePlanShape(t *testing.T) {
	raw, err := os.ReadFile("testdata/jake-channel-plan.json")
	if err != nil {
		t.Fatalf("the station plan fixture is missing: %v", err)
	}
	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("the plan document does not parse: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("the plan document is not valid: %v", err)
	}

	start := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	sources := []Source{}
	episodes := map[string][]catalog.PodcastEpisode{}

	// Nineteen podcast subscriptions, tiered the way his are, with a mix of
	// brand-new episodes (owed) and back catalogue.
	tiers := []string{"S", "A", "A", "B", "B", "B", "B", "", "", "", "", "", "", "", "", "", "", "", ""}
	for index, tier := range tiers {
		id := "p" + strconv.Itoa(index)
		src := podcastSource("pod"+strconv.Itoa(index), "Show "+strconv.Itoa(index), id)
		if tier != "" {
			src.Config["tier"] = tier
		}
		sources = append(sources, src)
		list := []catalog.PodcastEpisode{}
		// One fresh episode each, dropped overnight.
		list = append(list, episode(id+"-new", "Show "+strconv.Itoa(index)+" — today",
			start.Add(-6*time.Hour), 40+index%4*15))
		for back := 0; back < 15; back++ {
			list = append(list, episode(id+"-"+strconv.Itoa(back),
				"Show "+strconv.Itoa(index)+" archive "+strconv.Itoa(back),
				start.AddDate(0, 0, -30-back), 30+back%3*14))
		}
		episodes[id] = list
	}

	// Hardcore History, twice over: the on-disk copy and the RSS feed.
	giants := []catalog.PodcastEpisode{}
	for index := 0; index < 6; index++ {
		giants = append(giants, episode("hh-"+strconv.Itoa(index),
			"Mania for Subjugation "+strconv.Itoa(index),
			start.AddDate(0, 0, -200-index*40), 231+index*20))
	}
	episodes["phh"] = giants
	sources = append(sources,
		podcastSource("hh-feed", "", "phh"),
		podcastSource("hh-disk", "", "phh"))

	// One music playlist, and the booked shows behind the slot blocks.
	songs := []catalog.MusicTrack{}
	for index := 0; index < 200; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%60), 180+index%7*20))
	}
	sources = append(sources, musicSource("csrc_b8e3ddd9ac1df4fe0677c01c", "House Playlist", "pl1"))
	for _, pool := range plan.Pools {
		for _, id := range pool.SourceIDs {
			sources = append(sources, Source{
				ID: id, ChannelID: "c", Kind: SourceLiveStream, Label: pool.Label,
				Enabled: true, Role: RoleShow,
				Config: map[string]any{"url": "http://example.test/" + id},
			})
		}
	}

	engine := &Engine{
		Plan: plan, Channel: Channel{ID: "jake", DayStartMinute: 8 * 60, DayEndMinute: 23 * 60},
		Sources: sources, History: NewMemoryHistory(), Obligations: NewMemoryObligations(),
		Catalog:  &stubCatalog{episodes: episodes, playlists: map[string][]catalog.MusicTrack{"pl1": songs}},
		Skips:    NewSkipRegistry(func() time.Time { return start }),
		Location: time.UTC,
	}
	result, err := Simulate(context.Background(), engine,
		SimOptions{Start: start, Duration: 21 * 24 * time.Hour, MaxSteps: 40000})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}

	if len(result.Gaps) > 0 {
		t.Fatalf("%d moments with nothing to play, first: %s", len(result.Gaps), result.Gaps[0].Reason)
	}

	giantAirings, giantTime, cut := 0, time.Duration(0), 0
	total, musicTime := time.Duration(0), time.Duration(0)
	shows := 0
	for _, step := range result.Steps {
		total += step.Length
		if step.Item.Category == "music" {
			musicTime += step.Length
		}
		if step.Item.SourceID == "hh-feed" || step.Item.SourceID == "hh-disk" {
			giantAirings++
			giantTime += step.Length
			if step.Item.DurationSeconds > 0 &&
				step.Length < time.Duration(step.Item.DurationSeconds)*time.Second-time.Minute {
				cut++
			}
		}
		// A booked show must only ever air inside its own slot.
		if step.Decision.BlockID == "general" || step.Decision.BlockID == "fresh" {
			for _, src := range sources {
				if src.ID == step.Item.SourceID && src.Role == RoleShow {
					shows++
				}
			}
		}
	}
	t.Logf("21 days: %s of programming, music %.0f%%",
		total.Round(time.Minute), float64(musicTime)/float64(total)*100)
	t.Logf("Hardcore History: %d airings, %s, %d cut short",
		giantAirings, giantTime.Round(time.Minute), cut)
	if shows > 0 {
		t.Fatalf("%d booked-show items aired in the general rotation", shows)
	}
	if cut > 0 {
		t.Fatalf("%d long episodes were cut off by a booked show", cut)
	}
	if giantAirings == 0 {
		t.Fatalf("Hardcore History never played in three weeks")
	}
	if share := float64(giantTime) / float64(total); share > 0.10 {
		t.Fatalf("Hardcore History took %.0f%% of three weeks", share*100)
	}

	// A real music block every day, not just songs between podcasts.
	musicHour := map[string]time.Duration{}
	for _, step := range result.Steps {
		if step.Decision.BlockID == "music-hour" {
			musicHour[step.At.Format("2006-01-02")] += step.Length
		}
	}
	if len(musicHour) < 20 {
		t.Fatalf("only %d of 21 days had a music hour", len(musicHour))
	}
	shortest := time.Duration(0)
	for _, aired := range musicHour {
		if shortest == 0 || aired < shortest {
			shortest = aired
		}
	}
	t.Logf("music hour ran on %d/21 days, shortest %s", len(musicHour), shortest.Round(time.Minute))
	if shortest < 45*time.Minute {
		t.Fatalf("the shortest music hour was only %s", shortest.Round(time.Minute))
	}

	// The shows worth not missing get surfaced twice; ordinary ones once.
	airings := map[string]int{}
	for _, step := range result.Steps {
		if step.Item.ItemRef != "" {
			airings[step.Item.ItemRef]++
		}
	}
	topTwice, topOnce := 0, 0
	for ref, count := range airings {
		// Show 0 is S, shows 1 and 2 are A — their "today" episodes.
		if ref == "episode:p0-new" || ref == "episode:p1-new" || ref == "episode:p2-new" {
			if count >= 2 {
				topTwice++
			} else {
				topOnce++
			}
		}
	}
	t.Logf("top-tier new episodes aired twice: %d, once only: %d", topTwice, topOnce)
	if topTwice == 0 {
		t.Fatalf("no top-tier episode was surfaced twice")
	}

	// The shape of a morning: the day opens with music, then what is owed.
	t.Logf("first twelve items of the listening day:")
	for index, step := range result.Steps {
		if index >= 12 {
			break
		}
		t.Logf("  %s %-8s %-6s %-7s %s", step.At.Format("Mon 15:04"), step.Decision.BlockID,
			step.Item.Category, step.Length.Round(time.Minute), step.Item.Title)
	}
}
