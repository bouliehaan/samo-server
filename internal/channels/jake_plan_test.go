package channels

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
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
		// New episodes keep ARRIVING, at his measured rate: 21 a week across
		// nineteen shows. A fixture that drops everything on day one and then
		// goes quiet empties the obligation queue for good, which makes the
		// station look like it lives in the archive when in reality it barely
		// gets there — and every share measured off that is wrong.
		for drop := 0; drop < 8; drop++ {
			list = append(list, episode(
				id+"-new"+strconv.Itoa(drop),
				"Show "+strconv.Itoa(index)+" — drop "+strconv.Itoa(drop),
				start.Add(time.Duration(drop)*time.Duration(19*24/3)*time.Hour).Add(-6*time.Hour),
				40+index%4*15))
		}
		for back := 0; back < 15; back++ {
			list = append(list, episode(id+"-"+strconv.Itoa(back),
				"Show "+strconv.Itoa(index)+" archive "+strconv.Itoa(back),
				start.AddDate(0, 0, -30-back), 30+back%3*14))
		}
		episodes[id] = list
	}

	// Rogan: the hardest thing on the station to schedule. Three hours, four a
	// week, dropping late morning — long enough that only the morning window
	// can hold him, so where the music block sits decides whether Jacob hears
	// him the day he drops or the day after.
	rogan := podcastSource("rogan", "The Joe Rogan Experience", "prog")
	rogan.Config["tier"] = "B"
	sources = append(sources, rogan)
	roganEps := []catalog.PodcastEpisode{}
	for drop := 0; drop < 12; drop++ {
		// 11:30 local, every other day or so.
		at := start.AddDate(0, 0, drop*2).Add(3*time.Hour + 30*time.Minute)
		roganEps = append(roganEps, episode("rog-"+strconv.Itoa(drop),
			"JRE #"+strconv.Itoa(2200+drop), at, 180))
	}
	episodes["prog"] = roganEps

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
	// His real playlist's shape, measured: 372 tracks, and crucially a TAIL of
	// short ones — 5 under a minute, 31 more under two. A fixture where every
	// track is over three minutes cannot fill the last ninety seconds of a
	// booked block, and invents dead air the real station would not have.
	songs := []catalog.MusicTrack{}
	lengths := []int{}
	for _, band := range []struct{ count, secs int }{
		{5, 45}, {31, 95}, {169, 160}, {99, 200}, {50, 260}, {12, 320}, {3, 380}, {3, 500},
	} {
		for i := 0; i < band.count; i++ {
			lengths = append(lengths, band.secs)
		}
	}
	for index, secs := range lengths {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			// A third of the library is one artist, as his is.
			map[bool]string{true: "Elvis Presley", false: "Artist " + strconv.Itoa(index%114)}[index%3 == 0],
			secs))
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
		for i, g := range result.Gaps {
			if i >= 6 {
				break
			}
			t.Logf("   GAP %s :: %s", g.At.Format("Mon 02 Jan 15:04"), g.Reason)
		}
		t.Fatalf("%d moments with nothing to play", len(result.Gaps))
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
	// A giant is welcome on a quiet afternoon and never inside the new-episode
	// cycle: rest "never" on the station, relaxed to a fortnight in the archive
	// block, which by definition only runs when nothing is owed.
	t.Logf("giant airings over 21 days: %d (%s)", giantAirings, giantTime.Round(time.Minute))

	// How often does Rogan go out the DAY he drops?
	dropDay := map[string]string{}
	for _, e := range roganEps {
		dropDay["episode:"+e.ID] = e.PublishedAt.Format("2006-01-02")
	}
	sameDay, later, never := 0, 0, 0
	airedOn := map[string]string{}
	for _, step := range result.Steps {
		if _, ok := dropDay[step.Item.ItemRef]; ok {
			if _, seen := airedOn[step.Item.ItemRef]; !seen {
				airedOn[step.Item.ItemRef] = step.At.Format("2006-01-02")
			}
		}
	}
	for ref, dropped := range dropDay {
		switch aired, ok := airedOn[ref]; {
		case !ok:
			never++
		case aired == dropped:
			sameDay++
		default:
			later++
		}
	}
	t.Logf("ROGAN: same day %d | later %d | never %d (of %d drops)",
		sameDay, later, never, len(dropDay))
	// ROGAN same-day guard. Three hours dropping late morning only fits the
	// morning window, so this is really a test that the music block has not
	// crept in front of All Things Considered and halved it.
	if sameDay < len(dropDay)*3/4 {
		t.Fatalf("only %d of %d Rogan drops aired the same day — the morning window is too short",
			sameDay, len(dropDay))
	}
	if giantAirings > 3 {
		t.Fatalf("Hardcore History aired %d times in three weeks — over-represented", giantAirings)
	}
	if share := float64(giantTime) / float64(total); share > 0.10 {
		t.Fatalf("Hardcore History took %.0f%% of three weeks", share*100)
	}

	// How much of the day is spent with nothing owed — the only time a
	// back-catalogue giant would be welcome.
	byBlock := map[string]time.Duration{}
	for _, step := range result.Steps {
		byBlock[step.Decision.BlockID] += step.Length
	}
	for _, id := range []string{"fresh", "general", "music-hour"} {
		t.Logf("  block %-11s %s over 21 days (%.0f min/day)", id,
			byBlock[id].Round(time.Minute), byBlock[id].Minutes()/21)
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
	if shortest < 50*time.Minute {
		t.Fatalf("the shortest music block was only %s", shortest.Round(time.Minute))
	}

	// PUNCTUALITY. Over three weeks, against his real plan, every booked slot
	// has to open at the time it says.
	//
	// The station used to be reliably early: KRCC at 18:29:06 for an 18:30
	// slot, All Things Considered at 15:59:03, the 22:00 shows at 21:59:04.
	// Never a fault anybody logged — the appointment was brought forward on
	// purpose whenever the gap in front of it had closed to less than the
	// shortest thing the station owns, which is what a gap in front of an
	// appointment always closes to.
	report := buildSimReport(engine, result, start, start.Add(21*24*time.Hour))
	early, late, missed := 0, 0, 0
	worstEarly, worstLate := time.Duration(0), time.Duration(0)
	for _, anchor := range report.Anchors {
		if anchor.Missed {
			missed++
			t.Logf("   MISSED %s due %s", anchor.Label, anchor.Due.Format("Mon 02 15:04:05"))
			continue
		}
		switch drift := anchor.StartedAt.Sub(anchor.Due); {
		case drift < 0:
			early++
			if -drift > minBoundaryFill {
				t.Logf("   EARLY %-26s due %s, on air %s (%s early)", anchor.Label,
					anchor.Due.Format("Mon 02 15:04:05"), anchor.StartedAt.Format("Mon 02 15:04:05"),
					(-drift).Round(time.Second))
			}
			if -drift > worstEarly {
				worstEarly = -drift
			}
		case drift > 0:
			late++
			if drift > worstLate {
				worstLate = drift
			}
		}
	}
	t.Logf("punctuality over %d booked slots: %d early (worst %s), %d late (worst %s), %d missed",
		len(report.Anchors), early, worstEarly.Round(time.Second), late, worstLate.Round(time.Second), missed)
	if missed > 0 {
		t.Fatalf("%d booked slots never went on air", missed)
	}
	// Early only ever by a gap too small to hold anything, and never late: a
	// slot that starts late is one whose opening the listener has missed.
	if worstEarly > minBoundaryFill {
		t.Fatalf("a booked slot opened %s early; nothing may come forward by more than the %s"+
			" gap that is too small to fill", worstEarly.Round(time.Second), minBoundaryFill)
	}
	if worstLate > 0 {
		t.Fatalf("a booked slot opened %s late", worstLate.Round(time.Second))
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
		if strings.HasPrefix(ref, "episode:p0-new") ||
			strings.HasPrefix(ref, "episode:p1-new") ||
			strings.HasPrefix(ref, "episode:p2-new") {
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
