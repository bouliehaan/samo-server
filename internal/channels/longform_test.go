package channels

import (
	"context"
	"slices"
	"strconv"
	"strings"
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

// The short episode of an enormous show.
//
// Taken from the real station, 2026-08-11 13:12. Hardcore History had been on
// air forty-two hours earlier and was resting for three weeks. Every giant in
// the feed was refused for exactly that reason — "4h1m long, and this show
// aired 42h8m ago (a giant rests 504h)" — and then the station played
// "Darkness Buries the Bronze Age", thirty-four minutes, Hardcore History, out
// of the same feed, over thirty other shows that were all clean.
//
// The rationing gated on the length of the episode in hand, so the show's short
// pieces never reached the rest they were sitting inside. Four new episodes were
// owed at the time and all four genuinely overran the booked hour ahead, so this
// is not about obligations: it is about which back catalogue gets the slot.
func TestShortEpisodeOfARestingShowStaysOff(t *testing.T) {
	// 15:12, forty-eight minutes before the booked afternoon slot — the same
	// shape of hole the real decision was filling.
	start := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 10, 15, 12, 0, 0, time.UTC)

	engine := longFormStation(t, start, false)
	engine.Plan.LongForm = LongFormPolicy{Threshold: "2h", Rest: "21d"}
	if err := engine.Plan.Validate(); err != nil {
		t.Fatalf("plan: %v", err)
	}

	// The half-hour piece, in among the epics.
	stub := engine.Catalog.(*stubCatalog)
	stub.episodes["phh"] = append(stub.episodes["phh"],
		episode("hh-short", "Darkness Buries the Bronze Age", start.AddDate(0, 0, -400), 34))

	// The giant that aired, forty-two hours ago.
	aired := now.Add(-42 * time.Hour)
	engine.History.(*MemoryHistory).Record(MemoryPlay{
		SourceID: "carlin", ItemRef: "episode:hh-0", Category: "talk",
		StartedAt: aired.Add(-4 * time.Hour), EndedAt: aired,
	})

	item, decision, _, err := engine.Decide(context.Background(), now, ProgramState{})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if item.SourceID == "carlin" {
		t.Fatalf("played %q from a show that aired 42h ago and rests for 21 days;"+
			" its own giants were refused in the same decision\nreasons: %s",
			item.Title, rejectionsFor(decision, "carlin"))
	}
	if item.ItemRef == "" {
		t.Fatalf("no item at all — the station went quiet rather than playing one of the clean shows")
	}
	t.Logf("played %q from %s instead", item.Title, item.SourceID)
}

// Across three weeks: once a giant of a show has been on, nothing of that
// show's goes out again until it has been quiet for what the giant cost —
// a day per hour on air. Length is not the question. The listener hears a show.
//
// Measured only over decisions the station made freely. The rule sits near the
// bottom of the relaxation ladder, not above it: when a thin library has
// eliminated every other candidate the engine gives the rule up rather than go
// silent, and that is deliberate. What must never happen — and is what happened
// on the real station — is the show returning while there were clean
// alternatives sitting right there.
func TestARestingShowRestsWholly(t *testing.T) {
	start := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	engine := longFormStation(t, start, false)
	engine.Plan.LongForm = LongFormPolicy{Threshold: "2h", Rest: "21d"}
	stub := engine.Catalog.(*stubCatalog)
	for index := 0; index < 3; index++ {
		stub.episodes["phh"] = append(stub.episodes["phh"],
			episode("hh-short-"+strconv.Itoa(index), "Addendum "+strconv.Itoa(index),
				start.AddDate(0, 0, -400-index), 30+index*6))
	}

	result, err := Simulate(context.Background(), engine,
		SimOptions{Start: start, Duration: 21 * 24 * time.Hour, MaxSteps: 40000})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}

	// The quiet a giant buys runs from when it FINISHED, and is measured
	// against what that giant actually cost.
	var quietUntil time.Time
	var owed, after string
	violations := []string{}
	airings, conceded := 0, 0
	for _, step := range result.Steps {
		if step.Item.SourceID != "carlin" {
			continue
		}
		airings++
		if step.At.Before(quietUntil) {
			if slices.Contains(step.Decision.Relaxed, "longFormRationing") {
				conceded++
			} else {
				violations = append(violations, step.At.Format("Jan 2 15:04")+" "+
					step.Item.Title+" — "+quietUntil.Sub(step.At).Round(time.Minute).String()+
					" short of the "+owed+" owed after "+after)
			}
		}
		// Only a giant buys quiet. An ordinary half-hour episode of an
		// enormous show is an ordinary episode.
		if step.Length < 2*time.Hour {
			continue
		}
		if quiet := showQuietAfter(step.Length, 21*24*time.Hour); quiet > 0 {
			if until := step.Ends.Add(quiet); until.After(quietUntil) {
				quietUntil, owed, after = until, quiet.Round(time.Hour).String(), step.Item.Title
			}
		}
	}
	t.Logf("Hardcore History over 21 days: %d airings, %d of them with the rule given up "+
		"because nothing else qualified at all", airings, conceded)
	if len(violations) > 0 {
		t.Fatalf("the show came back before it had been quiet, with other shows available:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// One long episode does not make a show a long-form show.
//
// Measured from the real library: of eighteen podcasts, fourteen have at least
// one episode over two hours — The Dude Grows Show has four hundred and
// ninety-nine ordinary episodes and exactly one that runs long. Rationing a
// show because its catalogue CONTAINS a giant would rest almost the whole
// station for three weeks at a time and empty the archive.
//
// So the rest is charged to the airing, not to the show's habits: it starts
// when a giant actually takes three hours of somebody's day, and until then a
// mostly-ordinary show is ordinary programming.
func TestOneLongEpisodeDoesNotRationTheWholeShow(t *testing.T) {
	start := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	engine := longFormStation(t, start, false)
	engine.Plan.LongForm = LongFormPolicy{Threshold: "2h", Rest: "21d"}

	// The Dude Grows Show, in miniature: a deep archive of ordinary episodes
	// and a single outlier that happens to run long.
	stub := engine.Catalog.(*stubCatalog)
	engine.Sources = append(engine.Sources, podcastSource("dude", "The Dude Grows Show", "pdude"))
	list := []catalog.PodcastEpisode{episode("dude-big", "The Dude Grows Show marathon",
		start.AddDate(0, 0, -300), 150)}
	for index := 0; index < 40; index++ {
		list = append(list, episode("dude-"+strconv.Itoa(index),
			"The Dude Grows Show "+strconv.Itoa(index),
			start.AddDate(0, 0, -40-index), 45))
	}
	stub.episodes["pdude"] = list

	result, err := Simulate(context.Background(), engine,
		SimOptions{Start: start, Duration: 7 * 24 * time.Hour, MaxSteps: 20000})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	airings := 0
	for _, step := range result.Steps {
		if step.Item.SourceID == "dude" {
			airings++
		}
	}
	t.Logf("The Dude Grows Show over 7 days: %d airings", airings)
	// A show rationed as a giant would manage one airing in three weeks. The
	// other five podcasts each get a share of the week, and this one has the
	// deepest archive of any of them.
	if airings < 5 {
		t.Fatalf("a show with one long episode among forty-one aired only %d times in a week —"+
			" it is being rationed as though it were Hardcore History", airings)
	}
}

// rejectionsFor is what the decision said about a source, so a failure names the
// rule rather than leaving the reader to guess which one let the item through.
func rejectionsFor(decision Decision, sourceID string) string {
	out := []string{}
	for _, rejection := range decision.Rejected {
		out = append(out, rejection.Rule+": "+rejection.Reason)
	}
	if len(out) == 0 {
		return "none recorded (nothing from " + sourceID + " was refused at all)"
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return strings.Join(out, "; ")
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

// A flex music block instead of a booked music hour.
//
// Jacob: "maybe we shouldn't hardcode a music hour? Maybe we just have a flex
// music block where up to once a day, if we have a time slot that requires it,
// we just play music for 20-30 minutes. We just keep running into this issue
// where the music hour is really fucking up the scheduling."
//
// He is right about the mechanism. A booked hour is an anchor, and anchors are
// what refuse his episodes: every owed thing he has runs 70 to 175 minutes, so
// an appointment in the middle of the afternoon chops the one long open stretch
// of the day into two that nothing he is owed can fit inside. The music was
// never the problem; the booking was.
//
// So: no clock time at all. It runs when the gap in front of the next booked
// show is too short to be worth starting a podcast in, and it runs once.
func TestFlexMusicBlockRunsWhenTheGapNeedsItAndOnlyOnce(t *testing.T) {
	start := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	engine := longFormStation(t, start, false)

	flex := Block{
		ID: "music-flex", Label: "Music",
		Enter: BlockEntry{Days: "*", When: "window < 45m", MaxPerDay: 1},
		Exit:  BlockExit{AtNextAnchor: true},
		Pools: []PoolRef{{Pool: "music"}},
		Next:  "general",
	}
	engine.Plan.Blocks = append(engine.Plan.Blocks, flex)
	if err := engine.Plan.Validate(); err != nil {
		t.Fatalf("the flex block is not a valid plan: %v", err)
	}

	result, err := Simulate(context.Background(), engine,
		SimOptions{Start: start, Duration: 5 * 24 * time.Hour, MaxSteps: 30000})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}

	perDay := map[string]int{}
	entries := map[string]bool{}
	for _, step := range result.Steps {
		if step.Item.BlockID != "music-flex" {
			continue
		}
		day := step.At.Format("2006-01-02")
		// One ENTRY per day, not one item — a block plays a set.
		key := day + "|" + step.Decision.EnteredAt.Format(time.RFC3339)
		if !entries[key] {
			entries[key] = true
			perDay[day]++
		}
	}
	if len(perDay) == 0 {
		t.Fatalf("the flex block never ran in five days — a condition that is never"+
			" true is a block that does not exist\n%s", result.Format(false))
	}
	for day, count := range perDay {
		if count > 1 {
			t.Fatalf("the flex block ran %d times on %s, and it is capped at once a day",
				count, day)
		}
	}
	t.Logf("flex music ran on %d of 5 days, at most once each", len(perDay))
}
