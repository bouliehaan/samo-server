package channels

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The mornings of 2026-09-14 and -15, from the play log, verbatim:
//
//	08:57:15  Evening Hymns — Intro                       (Morning Music Block)
//	08:59:23  Stavvy's World — #198 - Nikki Glaser         15s of 1h42m
//	08:59:38  sombr — 12 to 12                             22s, cut on the hour
//	09:00:00  Bruno Mars — I Just Might                    18s, skipped
//	09:00:18  Depeche Mode — Enjoy the Silence              8s, skipped
//	09:00:26  The Harland Highway — MATAN introduces …     the first real programme
//
// Four things went wrong in ninety seconds, and every one of them is a rule
// about a boundary: the last of a booked hour was handed to a programme that
// could not fit it; that programme's own watchdog then cut it, because the
// hour it had been released from was still on the timeline; the fifteen-second
// airing was written up as one; and the block that took over at the top of the
// hour opened with a music break on top of an hour of music. These are the
// four invariants, each against the plan document his station runs today.

// jakeChannelPlan2026_09_17 is the plan as it is live on 2026-09-17: the
// Morning Music Block 08:00–09:00, the new-episodes block whose cycle opens
// with a break, and All Things Considered at 16:00 on weekdays.
func jakeChannelPlan2026_09_17(t *testing.T) Plan {
	t.Helper()
	raw, err := os.ReadFile("testdata/jake-channel-plan-2026-09-17.json")
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
	return plan
}

const (
	morningMusicBlock = "slot-csched_7f870fc70320ed62041e6d16"
	morningPlaylist   = "csrc_2fae1d5632b01252a74a62bb"
	allThingsBlock    = "slot-csched_284a41a581ede1252df1f323"
	stavvysWorld      = "csrc_b8b9ff572856530122bdc4dd"
	harlandHighway    = "csrc_5d10bf8d06938a274c662cc9"
	lexFridman        = "csrc_796942f0527c302b71c02bf3"
	tetragrammaton    = "csrc_dcd6f497cbc6b0fc5e7bd481"
	explorePlaylist   = "csrc_5e29bee549e8b201e8692a48"
)

// jakeStation is his station as of the incident: his plan, his playlist's
// shape (a tail of short tracks, none under forty-five seconds), his booked
// shows as relays, and the podcasts that were owed that week — including Lex
// Fridman, whose feed never says how long an episode is.
func jakeStation(t *testing.T, now time.Time) *station {
	t.Helper()
	plan := jakeChannelPlan2026_09_17(t)

	songs := []catalog.MusicTrack{}
	index := 0
	for _, band := range []struct{ count, secs int }{{5, 45}, {10, 95}, {30, 160}, {10, 200}, {5, 260}} {
		for i := 0; i < band.count; i++ {
			songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
				"Artist "+strconv.Itoa(index%20), band.secs))
			index++
		}
	}

	sources := []Source{}
	for _, pool := range plan.Pools {
		for _, id := range pool.SourceIDs {
			if id == morningPlaylist {
				// The music hour is his playlist booked as a show: a
				// music-playlist source with the show role, exactly as the
				// channel carries it.
				src := musicSource(id, "Morning Music Block", "pl1")
				src.Role = RoleShow
				sources = append(sources, src)
				continue
			}
			sources = append(sources, Source{
				ID: id, ChannelID: "ch1", Kind: SourceLiveStream, Label: pool.Label,
				Enabled: true, Role: RoleShow,
				Config: map[string]any{"url": "http://example.test/" + id},
			})
		}
	}
	sources = append(sources, musicSource(explorePlaylist, "Explore", "pl1"))

	stavvys := podcastSource(stavvysWorld, "Stavvy's World", "p-stavvys")
	stavvys.Config["tier"] = "S"
	harland := podcastSource(harlandHighway, "The Harland Highway", "p-harland")
	harland.Config["tier"] = "A"
	tetra := podcastSource(tetragrammaton, "Tetragrammaton with Rick Rubin", "p-tetra")
	tetra.Config["tier"] = "A"
	lex := podcastSource(lexFridman, "Lex Fridman Podcast", "p-lex")
	lex.Config["tier"] = "S"
	sources = append(sources, stavvys, harland, tetra, lex)

	episodes := map[string][]catalog.PodcastEpisode{
		"p-stavvys": {
			// #198, 1h42m10s, published the morning before: owed.
			episode("stavvys-198", "#198 - Nikki Glaser and JP McDade", now.Add(-23*time.Hour), 102),
			episode("stavvys-197", "#197", now.AddDate(0, 0, -8), 98),
			episode("stavvys-196", "#196", now.AddDate(0, 0, -15), 95),
		},
		"p-harland": {
			episode("harland-matan", "MATAN introduces SPIDER-GIRL to the world!", now.Add(-40*time.Hour), 77),
			episode("harland-old", "An older Harland", now.AddDate(0, 0, -9), 70),
		},
		"p-tetra": {
			episode("tetra-jay", "JAŸ-Z in 8: Inside the Series", now.Add(-8*time.Hour), 25),
			episode("tetra-old", "An older Tetragrammaton", now.AddDate(0, 0, -12), 60),
		},
		"p-lex": {
			// The feed carries no <itunes:duration>: every episode is 0s long
			// as far as the catalog knows, and the real ones run three hours.
			unmeasuredEpisode("lex-500", "#500 – Khabib Nurmagomedov", now.Add(-20*time.Hour)),
			unmeasuredEpisode("lex-499", "#499", now.AddDate(0, 0, -6)),
			unmeasuredEpisode("lex-498", "#498", now.AddDate(0, 0, -11)),
		},
	}
	cat := &stubCatalog{episodes: episodes, playlists: map[string][]catalog.MusicTrack{"pl1": songs}}
	return newStation(t, plan, sources, cat, now)
}

func unmeasuredEpisode(id, title string, published time.Time) catalog.PodcastEpisode {
	ep := episode(id, title, published, 0)
	ep.DurationSeconds = 0
	return ep
}

// inTheMusicHour puts the station where it was at the end of the music block:
// an hour in, its last song just finished.
func (s *station) inTheMusicHour(entered time.Time, lastSong time.Duration) {
	s.state = ProgramState{BlockID: morningMusicBlock, EnteredAt: entered, ItemCount: 15}
	s.history.Record(MemoryPlay{
		SourceID: morningPlaylist, ItemRef: "track:t-last", Artist: "Evening Hymns",
		Category:  "talk", // a playlist booked as a show is categorised as its role says
		StartedAt: s.now.Add(-lastSong), EndedAt: s.now, DurationSeconds: int(lastSong / time.Second),
	})
}

// Invariant 1: never start a programme in a tail it will be cut in.
//
// 08:59:23, thirty-seven seconds left of the music hour, nothing on the
// playlist that short. The hour is held with one more song faded out on the
// boundary — not handed to a ninety-minute podcast that then dies at fifteen
// seconds.
func TestTheLastOfAMusicHourIsHeldWithASongNotHandedToAPodcast(t *testing.T) {
	now := time.Date(2026, 9, 15, 8, 59, 23, 0, time.UTC) // Tuesday
	s := jakeStation(t, now)
	s.inTheMusicHour(now.Add(-59*time.Minute-23*time.Second), 128*time.Second)

	item, decision := s.decide()
	if decision.BlockID != morningMusicBlock {
		t.Fatalf("the last 37s of the music hour went to block %q with %q:\n%s",
			decision.BlockID, item.Title, decision.Explain())
	}
	if item.Kind == SourcePodcastSubscription {
		t.Fatalf("a podcast was started 37s before the hour: %q\n%s", item.Title, decision.Explain())
	}
	if item.SourceID != morningPlaylist {
		t.Fatalf("the hour was held with %q from %s, not one more of its own songs", item.Title, item.SourceID)
	}
	if item.MaxDuration != 37*time.Second || item.FadeOut <= 0 {
		t.Fatalf("the song must run to the hour and fade: got max %s, fade %s", item.MaxDuration, item.FadeOut)
	}
}

// Invariant 1, the other shape: a programme whose length nobody knows.
//
// 2026-09-16 15:59:55, five seconds before All Things Considered: a Lex Fridman
// episode — no duration in the feed, so "fits anywhere" — was started, capped to
// the five seconds, and logged as a four-second airing. An unmeasured programme
// is fitted as long as its kind of programming usually runs, and five seconds
// is not that.
func TestAnUnmeasuredProgrammeIsNotStartedIntoASliver(t *testing.T) {
	now := time.Date(2026, 9, 16, 15, 59, 55, 0, time.UTC) // Wednesday
	s := jakeStation(t, now)
	s.state = ProgramState{BlockID: "general", EnteredAt: now.Add(-3 * time.Hour), ItemCount: 6}
	s.history.Record(MemoryPlay{
		SourceID: explorePlaylist, ItemRef: "track:t-prev", Category: "music",
		StartedAt: now.Add(-178 * time.Second), EndedAt: now, DurationSeconds: 178,
	})

	item, decision := s.decide()
	if item.Kind == SourcePodcastSubscription {
		t.Fatalf("a programme was started five seconds before the news: %q (max %s)\n%s",
			item.Title, item.MaxDuration, decision.Explain())
	}
	// Five seconds is below anything worth filling, so the appointment opens.
	if decision.BlockID != allThingsBlock {
		t.Fatalf("expected All Things Considered to open across the 5s sliver, got block %q with %q:\n%s",
			decision.BlockID, item.Title, decision.Explain())
	}
	for _, rejection := range decision.Rejected {
		if rejection.Ref == "episode:lex-500" && rejection.Rule != ruleFitsBeforeAnchor {
			t.Fatalf("the unmeasured episode was refused by %q, not by the room: %s", rejection.Rule, rejection.Reason)
		}
	}
}

// And the same episode an hour out: it is not five seconds that is the point,
// it is that "unmeasured" reads as "fits", and a three-hour show was being
// started fifty minutes before the news and cut at the hour, every time.
func TestAnUnmeasuredProgrammeIsFittedAsLongAsItsKindUsuallyRuns(t *testing.T) {
	now := time.Date(2026, 9, 16, 15, 6, 0, 0, time.UTC) // 54 minutes before ATC
	s := jakeStation(t, now)
	s.state = ProgramState{BlockID: "fresh", EnteredAt: now.Add(-2 * time.Hour), ItemCount: 3, PatternIndex: 1}
	s.history.Record(MemoryPlay{
		SourceID: explorePlaylist, ItemRef: "track:t-prev", Category: "music",
		StartedAt: now.Add(-178 * time.Second), EndedAt: now, DurationSeconds: 178,
	})

	item, decision := s.decide()
	if item.SourceID == lexFridman {
		t.Fatalf("an episode of unknown length was started 54 minutes before the news: %q\n%s",
			item.Title, decision.Explain())
	}
	// Something owed that fits goes out instead: the 25-minute Tetragrammaton.
	if item.ItemRef != "episode:tetra-jay" {
		t.Fatalf("expected the owed 25-minute episode in the 54 minutes left, got %q:\n%s",
			item.Title, decision.Explain())
	}
	refused := false
	for _, rejection := range decision.Rejected {
		if rejection.Ref == "episode:lex-500" && rejection.Rule == ruleFitsBeforeAnchor {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("the record does not say the unmeasured episode was refused for the room:\n%s", decision.Explain())
	}
}

// Invariant 3: an appointment the engine released must not take the air back.
//
// 08:59:52, seven seconds left — under anything worth filling — so the hour is
// released to the block the schedule hands to at 09:00 anyway. The release is
// written into the state: the next decision before the hour turns continues in
// the released block, and the cut-in watchdog is told the appointment is over.
func TestAReleasedHourIsNotTakenBackBeforeItTurns(t *testing.T) {
	now := time.Date(2026, 9, 17, 8, 59, 52, 0, time.UTC) // Thursday
	s := jakeStation(t, now)
	s.inTheMusicHour(now.Add(-59*time.Minute-52*time.Second), 128*time.Second)

	item, decision, next, err := s.engine.Decide(context.Background(), s.now, s.state)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	// Released to the block that would take over at 09:00 — the new-episodes
	// block, not the default one it used to fall into for lack of a condition.
	if decision.BlockID != "fresh" {
		t.Fatalf("the last 7s were handed to %q, not to the new-episodes block:\n%s",
			decision.BlockID, decision.Explain())
	}
	if item.Kind != SourcePodcastSubscription {
		t.Fatalf("the new-episodes block opened with %q (%s), not an owed episode", item.Title, item.Kind)
	}
	if next.ReleasedAnchor != morningMusicBlock || !next.ReleasedUntil.Equal(now.Add(8*time.Second)) {
		t.Fatalf("the release is not on record: anchor %q until %s", next.ReleasedAnchor, next.ReleasedUntil)
	}
	// Bounded, if at all, by the next appointment seven hours away — never
	// by the eight seconds left of the hour it was released from.
	if item.MaxDuration > 0 && item.MaxDuration < time.Hour {
		t.Fatalf("the released item is capped at %s; the hour it was released from must not bound it", item.MaxDuration)
	}

	// The watchdog's question, three seconds later: is an appointment cutting
	// in on this? No — it was released.
	timeline := BuildTimeline(s.engine.Plan, now.Add(3*time.Second), time.UTC)
	if timeline.Active == nil || timeline.Active.BlockID != morningMusicBlock {
		t.Fatalf("the timeline should still show the music hour on air at 08:59:55")
	}
	if !next.released(timeline.Active, now.Add(3*time.Second)) {
		t.Fatal("the released hour reads as cutting in on the item it was released to")
	}
	// And once the hour has turned, the release is spent.
	if next.released(timeline.Active, now.Add(8*time.Second)) {
		t.Fatal("a release must not outlive the hour it released")
	}

	// The engine's own next decision before the hour turns: still the released
	// block, not the hour re-claiming what it gave up.
	s.state = next
	s.now = now.Add(3 * time.Second)
	_, again := s.decide()
	if again.BlockID != "fresh" {
		t.Fatalf("at 08:59:55 the music hour took the air back: block %q\n%s", again.BlockID, again.Explain())
	}
}

// Invariant 4: entering a podcast-first block from a music block must not open
// with a music break.
//
// 09:00:00, the music hour over, the new-episodes cycle [break, obligation]
// takes the hour. Its opening break would put two songs on top of an hour of
// songs; a break separates programming, and there is nothing here to separate.
// The cycle starts at the obligation instead.
func TestEnteringTheNewEpisodesBlockAfterMusicDoesNotOpenWithABreak(t *testing.T) {
	now := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC) // Tuesday
	s := jakeStation(t, now)
	s.inTheMusicHour(now.Add(-time.Hour), 22*time.Second)

	item, decision := s.decide()
	if decision.BlockID != "fresh" {
		t.Fatalf("expected the new-episodes block at 09:00, got %q:\n%s", decision.BlockID, decision.Explain())
	}
	if decision.Break != nil || item.Kind == SourceMusicPlaylist {
		t.Fatalf("the hour after the music block opened with a break: %q\n%s", item.Title, decision.Explain())
	}
	if item.Kind != SourcePodcastSubscription {
		t.Fatalf("expected an owed episode first, got %q (%s)", item.Title, item.Kind)
	}
}

// The break the cycle asks for is still a break when it follows talk: the rule
// is about what a break follows, not about the block's first item.
func TestABreakAfterTalkIsStillABreak(t *testing.T) {
	now := time.Date(2026, 9, 15, 10, 41, 0, 0, time.UTC)
	s := jakeStation(t, now)
	s.state = ProgramState{BlockID: "fresh", EnteredAt: now.Add(-100 * time.Minute), ItemCount: 1, PatternIndex: 2}
	s.history.Record(MemoryPlay{
		SourceID: harlandHighway, ItemRef: "episode:harland-matan", Category: "talk",
		StartedAt: now.Add(-77 * time.Minute), EndedAt: now, DurationSeconds: 77 * 60,
	})

	item, decision := s.decide()
	if decision.Break == nil || item.Kind != SourceMusicPlaylist {
		t.Fatalf("after 77 minutes of talk the cycle's break was passed over: %q\n%s", item.Title, decision.Explain())
	}
}

// The two skips. A break is planned as a unit and its remaining songs are
// queued; SKIP passes over the item and steps off its source for twenty
// minutes, then the decision runs again — and the queue served the break's
// second song from the very source the skip had just stepped off. The queue is
// re-validated against the world, and a skip is part of the world.
func TestASkipDropsTheRestOfAPlannedBreak(t *testing.T) {
	now := time.Date(2026, 9, 15, 9, 0, 18, 0, time.UTC)
	s := jakeStation(t, now)
	s.state = ProgramState{
		BlockID: "fresh", EnteredAt: now.Add(-18 * time.Second), ItemCount: 1, PatternIndex: 1, LastWasBreak: true,
		Queue: []QueuedItem{{SourceID: explorePlaylist, Ref: "track:t7", Reason: "the cycle calls for a break here", Position: 2, Of: 2}},
	}
	s.history.Record(MemoryPlay{
		SourceID: explorePlaylist, ItemRef: "track:t3", Category: "music",
		StartedAt: now.Add(-18 * time.Second), EndedAt: now, DurationSeconds: 18,
	})
	// What SKIP does before the decision runs again.
	s.engine.Skips.SuppressRef("ch1", "track:t3")
	s.engine.Skips.Suppress(explorePlaylist, skipSourceStepAside)

	item, decision := s.decide()
	if item.ItemRef == "track:t7" || item.Kind == SourceMusicPlaylist {
		t.Fatalf("the skipped break's second song went out anyway: %q\n%s", item.Title, decision.Explain())
	}
	if item.Kind != SourcePodcastSubscription {
		t.Fatalf("expected the cycle to move on to what is owed, got %q (%s)", item.Title, item.Kind)
	}
}

// Invariant 2: a false start earns nothing.
//
// What the fifteen-second airing actually wrote: a play-log row every
// separation rule then read as "this show aired just now", a few thousandths of
// a surfacing in credit, and a cycle moved past the position it never filled.
// The station's own clock cutting a programme before it amounted to an airing
// is not an airing; a listener's skip, a stall, a bag of songs faded on the
// boundary are all something else.
func TestAFalseStartIsTheStationsOwnCutBeforeAnythingAired(t *testing.T) {
	episode := PlaybackItem{Kind: SourcePodcastSubscription, ItemRef: "episode:stavvys-198", DurationSeconds: 6130}
	song := PlaybackItem{Kind: SourceMusicPlaylist, ItemRef: "track:t7", Shuffled: true, DurationSeconds: 243}
	relay := PlaybackItem{Kind: SourceInternetStation, ItemRef: "station:krcc", Live: true}

	cases := []struct {
		name      string
		item      PlaybackItem
		played    time.Duration
		completed bool
		cause     cutCause
		err       error
		want      bool
	}{
		{"the watchdog cut an episode at 15s", episode, 15 * time.Second, false, cutByPreempt, context.Canceled, true},
		{"a booked show cut in at 4s", episode, 4 * time.Second, false, cutByBoundary, context.Canceled, true},
		{"the play window closed at 5s", episode, 5 * time.Second, false, cutUnknown, context.DeadlineExceeded, true},
		{"the listener skipped it at 15s", episode, 15 * time.Second, false, cutBySkip, context.Canceled, false},
		{"the source stalled at 30s", episode, 30 * time.Second, false, cutByStall, context.Canceled, false},
		{"it was cut after it counted as aired", episode, 5 * time.Minute, false, cutByPreempt, context.Canceled, false},
		{"it ran to its own end", episode, 6130 * time.Second, true, cutUnknown, nil, false},
		{"a song faded on the boundary", song, 22 * time.Second, false, cutByBoundary, context.Canceled, false},
		{"a relay cut on the hour", relay, 30 * time.Second, false, cutByBoundary, context.Canceled, false},
		{"the decoder failed", episode, 10 * time.Second, false, cutUnknown, errors.New("exit status 1"), false},
	}
	for _, tc := range cases {
		if got := falseStart(tc.item, tc.played, tc.completed, tc.cause, tc.err); got != tc.want {
			t.Errorf("%s: falseStart = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The cause reaches the loop: a skip says so before it cancels, and the loop
// reads it exactly once.
func TestASkipSaysItWasASkip(t *testing.T) {
	streamer := skippingStreamer(t, PlaybackItem{
		Title: "#198", ItemRef: "episode:stavvys-198", SourceID: stavvysWorld,
		Kind: SourcePodcastSubscription, Category: "talk",
	}, &recordingRecorder{})
	if !streamer.skipCurrent() {
		t.Fatal("skip did not take")
	}
	if cause := streamer.takeCutCause(); cause != cutBySkip {
		t.Fatalf("the loop was told %v, want a skip", cause)
	}
	if cause := streamer.takeCutCause(); cause != cutUnknown {
		t.Fatalf("a cause read once must be spent, got %v", cause)
	}
}

// Three weeks of his live plan, end to end: every booked slot on its second,
// no programme started into a boundary that takes it, and no hour after the
// music block opening with a break.
func TestJakePlan2026_09_17BoundariesOverThreeWeeks(t *testing.T) {
	start := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC) // Monday
	s := jakeStation(t, start)
	result, err := Simulate(context.Background(), s.engine,
		SimOptions{Start: start, Duration: 21 * 24 * time.Hour, MaxSteps: 40000})
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	if len(result.Gaps) > 0 {
		g := result.Gaps[0]
		t.Fatalf("%d moments with nothing to play, first %s: %s", len(result.Gaps), g.At.Format("Mon 02 15:04:05"), g.Reason)
	}

	report := buildSimReport(s.engine, result, start, start.Add(21*24*time.Hour))
	for _, anchor := range report.Anchors {
		if anchor.Missed {
			t.Fatalf("%s due %s never went on air", anchor.Label, anchor.Due.Format("Mon 02 15:04:05"))
		}
		if drift := anchor.StartedAt.Sub(anchor.Due); drift > 0 || -drift > minBoundaryFill {
			t.Fatalf("%s due %s went on air at %s", anchor.Label,
				anchor.Due.Format("Mon 02 15:04:05"), anchor.StartedAt.Format("Mon 02 15:04:05"))
		}
	}

	falseStarts, breaksAfterMusic, heldHours := 0, 0, 0
	previousBlock := ""
	for _, step := range result.Steps {
		// A programme that went out for less than counts as an airing, cut by
		// the clock: the thing the boundary rules exist to prevent.
		if step.Item.Kind == SourcePodcastSubscription && step.Length < countsAsAired &&
			step.Item.MaxDuration > 0 && step.Length >= step.Item.MaxDuration {
			falseStarts++
			t.Logf("   FALSE START %s %q ran %s", step.At.Format("Mon 02 15:04:05"), step.Item.Title, step.Length)
		}
		// The first thing after the music block is never a break.
		if previousBlock == morningMusicBlock && step.Decision.BlockID != morningMusicBlock && step.Decision.Break != nil {
			breaksAfterMusic++
			t.Logf("   BREAK AFTER MUSIC %s %q", step.At.Format("Mon 02 15:04:05"), step.Item.Title)
		}
		if step.Decision.BlockID == morningMusicBlock && step.Item.FadeOut > 0 {
			heldHours++
		}
		previousBlock = step.Decision.BlockID
	}
	t.Logf("music hours held with a faded song: %d of 21; false starts: %d; breaks straight after the music block: %d",
		heldHours, falseStarts, breaksAfterMusic)
	if falseStarts > 0 {
		t.Fatalf("%d programmes were started into a boundary that cut them off", falseStarts)
	}
	if breaksAfterMusic > 0 {
		t.Fatalf("%d hours opened with a break on top of the music block", breaksAfterMusic)
	}
}

// A booked hour of programmes, not songs: its own pool cannot hold its tail
// without starting an episode to be cut off, so the tail goes to the pool the
// plan nominated for gaps instead. The hold refuses a programme; a song is
// what a gap is for.
func podcastHourPlan() Plan {
	return Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 1}, {ID: "music", Target: 0}},
		UnderrunPool: "music",
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
			{ID: "the-show", SourceIDs: []string{"show1"}},
		},
		Blocks: []Block{
			{ID: "general", Label: "General rotation", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "show-hour", Label: "The Show Hour",
				Enter: BlockEntry{At: "18:00", Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "19:00"},
				Next:  "general",
				Pools: []PoolRef{{Pool: "the-show"}}},
		},
	}
}

func TestTheTailOfAProgrammeHourIsNotFilledWithAProgrammeCutOff(t *testing.T) {
	now := time.Date(2026, 9, 15, 18, 52, 0, 0, time.UTC) // eight minutes left of the hour
	show := podcastSource("show1", "The Show", "p-show")
	show.Role = RoleShow
	episodes := []catalog.PodcastEpisode{}
	for index := 0; index < 6; index++ {
		episodes = append(episodes, episode("show-"+strconv.Itoa(index),
			"The Show "+strconv.Itoa(index), now.AddDate(0, 0, -7*index-1), 40+index*4))
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 20; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index), 180+index*5))
	}
	cat := &stubCatalog{
		episodes:  map[string][]catalog.PodcastEpisode{"p-show": episodes},
		playlists: map[string][]catalog.MusicTrack{"pl1": songs},
	}
	s := newStation(t, podcastHourPlan(), []Source{show, musicSource("mus1", "House", "pl1")}, cat, now)
	s.state = ProgramState{BlockID: "show-hour", EnteredAt: now.Add(-52 * time.Minute), ItemCount: 1}
	s.history.Record(MemoryPlay{
		SourceID: "show1", ItemRef: "episode:show-5", Category: "talk",
		StartedAt: now.Add(-52 * time.Minute), EndedAt: now, DurationSeconds: 52 * 60,
	})

	item, decision := s.decide()
	if item.Kind == SourcePodcastSubscription {
		t.Fatalf("an episode was started to be cut off eight minutes later: %q\n%s", item.Title, decision.Explain())
	}
	if decision.BlockID != "show-hour" || item.Category != "music" || item.MaxDuration != 8*time.Minute {
		t.Fatalf("expected the hour's last eight minutes filled with a song, got %q (%s in %s, max %s)",
			item.Title, item.Category, decision.BlockID, item.MaxDuration)
	}
	refused := 0
	for _, rejection := range decision.Rejected {
		if strings.HasPrefix(rejection.Ref, "episode:show-") {
			refused++
			if rejection.Rule != ruleFitsBeforeAnchor {
				t.Fatalf("the show's own episode was refused by %q, not by the room: %s", rejection.Rule, rejection.Reason)
			}
		}
	}
	if refused == 0 {
		t.Fatalf("the record does not say the show's own episodes were refused for the room:\n%s", decision.Explain())
	}
}

// The floor is the one place a stub is allowed: a station that owns nothing
// but programmes, with nothing that fits and nowhere to hand the time to,
// still plays something rather than going quiet. Anything beats dead air,
// including an episode cut off — and the record says the floor did it.
func TestTheFloorStillPrefersAStubToSilence(t *testing.T) {
	now := time.Date(2026, 9, 15, 18, 52, 0, 0, time.UTC)
	plan := podcastHourPlan()
	plan.UnderrunPool = ""
	// Booked back to back, so the eight minutes cannot be released to a
	// block with room: whatever takes the air is cut at 19:00 regardless.
	plan.Pools = append(plan.Pools, Pool{ID: "the-other-show", SourceIDs: []string{"show2"}})
	plan.Blocks = append(plan.Blocks, Block{ID: "other-hour", Label: "The Other Hour",
		Enter: BlockEntry{At: "19:00", Days: "*", Hard: true, Start: StartImmediately},
		Exit:  BlockExit{At: "20:00"},
		Next:  "general",
		Pools: []PoolRef{{Pool: "the-other-show"}}})
	show := podcastSource("show1", "The Show", "p-show")
	show.Role = RoleShow
	other := podcastSource("show2", "The Other Show", "p-other")
	other.Role = RoleShow
	episodes := []catalog.PodcastEpisode{}
	for index := 0; index < 6; index++ {
		episodes = append(episodes, episode("show-"+strconv.Itoa(index),
			"The Show "+strconv.Itoa(index), now.AddDate(0, 0, -7*index-1), 40+index*4))
	}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p-show":  episodes,
		"p-other": {episode("other-1", "The Other Show 1", now.AddDate(0, 0, -2), 55)},
	}}
	s := newStation(t, plan, []Source{show, other}, cat, now)
	s.state = ProgramState{BlockID: "show-hour", EnteredAt: now.Add(-52 * time.Minute), ItemCount: 1}

	item, decision := s.decide()
	if item.Kind != SourcePodcastSubscription || item.MaxDuration != 8*time.Minute || item.FadeOut <= 0 {
		t.Fatalf("with nothing else to reach for, expected an episode faded on the hour rather than silence, got %q (max %s, fade %s)\n%s",
			item.Title, item.MaxDuration, item.FadeOut, decision.Explain())
	}
	if len(decision.Relaxed) == 0 {
		t.Fatalf("a stub from the floor must be on the record as the floor:\n%s", decision.Explain())
	}
}
