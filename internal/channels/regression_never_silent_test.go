package channels

import (
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Three defects that all came out of the 2026-09-03 09:12 investigation, and
// all of which are about the station's own accounting rather than its taste.

// ---- the airing cap could not bite --------------------------------------

// `if c.Owed && int(c.Credit) < count { count = int(c.Credit) }` TRUNCATES, so
// anything owed below one full credit resolved to zero airings, mayAirAgain was
// handed zero, and it returns true unconditionally. The cap did not relax for
// those items; it switched off. An episode airing where nothing counts never
// accrues credit, so it could go round all day — the "don't play re-runs of
// episodes same-day" complaint, arrived at from the other end.
func TestAiringsThatReachedNobodyAreNotUnlimited(t *testing.T) {
	cases := []struct {
		name   string
		aired  int
		credit float64
		want   int
	}{
		// The case the credit clamp exists for: last night's airing must not
		// spend this morning's allowance.
		{"one airing nobody heard", 1, 0, 0},
		{"two airings nobody heard", 2, 0, 0},
		// But the allowance is finite. Beyond it the raw airtime is charged.
		{"three airings nobody heard", 3, 0, 1},
		{"six airings nobody heard", 6, 0, 4},
		// A partial airing is still a fraction, not a free pass.
		{"most of one airing", 1, 0.9, 0},
		{"most of four airings", 4, 3.9, 3},
		// Everything heard: the clamp does nothing at all.
		{"two full airings", 2, 2, 2},
		{"credit can never raise the count", 2, 9, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chargeableAirings(tc.aired, tc.credit); got != tc.want {
				t.Fatalf("%d airings with credit %v charged as %d, want %d",
					tc.aired, tc.credit, got, tc.want)
			}
			if got := chargeableAirings(tc.aired, tc.credit); got > tc.aired {
				t.Fatalf("credit raised the count from %d to %d; it may only ever lower it",
					tc.aired, got)
			}
		})
	}
}

// ---- the station is never silent ----------------------------------------

// When a block's pools go empty the engine used to return an error, and the
// streamer's answer to an error is to log it and sleep five seconds. Repeat and
// that is dead air on a loop — indistinguishable, from the listening end, from
// a crash.
func TestAStationWithAnEmptyBlockPoolStillPlays(t *testing.T) {
	now := time.Date(2026, 9, 3, 14, 0, 0, 0, time.UTC)

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		// The block can only reach a feed that has stopped resolving.
		Pools: []Pool{{ID: "talk", SourceIDs: []string{"dead"}}},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "talk", Weight: 1}},
		}},
	}

	songs := []catalog.MusicTrack{}
	for index := 0; index < 5; index++ {
		songs = append(songs, track(
			"t"+string(rune('a'+index)), "Song", "Artist "+string(rune('a'+index)), 210))
	}

	// The music source is enabled and playable, and no pool in the plan
	// mentions it. Under the old code that was silence.
	s := newStation(t, plan,
		[]Source{podcastSource("dead", "A feed that stopped", "gone"), musicSource("mus1", "House", "pl1")},
		&stubCatalog{
			episodes:  map[string][]catalog.PodcastEpisode{},
			playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		}, now)

	item, decision, err := s.tryDecide()
	if err != nil {
		t.Fatalf("the station went silent with five playable songs on the shelf: %v (%s)",
			err, decision.Error)
	}
	if item.SourceID != "mus1" {
		t.Fatalf("expected the fallback to reach the music source, got %q", item.SourceID)
	}
	// And it must say so: a station running on the floor should be visible in
	// the record, not merely audible.
	if len(decision.Relaxed) == 0 {
		t.Fatal("the station fell back to everything it owns and recorded no relaxation")
	}
}

// The floor is a floor, not a shortcut. With its own pools playable, a block
// programmes from them and nothing is relaxed.
func TestTheFallbackDoesNotFireWhenTheBlockCanProgramme(t *testing.T) {
	now := time.Date(2026, 9, 3, 14, 0, 0, 0, time.UTC)

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{"pod1"}}},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "talk", Weight: 1}},
		}},
	}

	s := newStation(t, plan,
		[]Source{podcastSource("pod1", "The Show", "p1"), musicSource("mus1", "House", "pl1")},
		&stubCatalog{
			episodes: map[string][]catalog.PodcastEpisode{
				"p1": {episode("ep1", "An episode", now.AddDate(0, 0, -40), 40)},
			},
			playlists: map[string][]catalog.MusicTrack{"pl1": {track("t1", "Song", "Artist", 210)}},
		}, now)

	item, decision := s.decide()
	if item.SourceID != "pod1" {
		t.Fatalf("the block could programme from its own pool but played %q", item.SourceID)
	}
	for _, relaxed := range decision.Relaxed {
		if relaxed == "the block's own pools" {
			t.Fatal("the last resort fired while the block's own pool was playable")
		}
	}
}

// ---- the run-up to a held episode ---------------------------------------

// heldStation is a show with a new episode that will be released when the
// listening day opens, plus that show's back catalogue and some music.
func heldStation(t *testing.T, now time.Time, published time.Time) *station {
	t.Helper()

	plan := Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 0.5}, {ID: "music", Target: 0.5}},
		ListeningDay: &DaySpec{Start: "08:00", End: "23:00"},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"pod1"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
		},
		Blocks: []Block{{
			ID: "overnight", Label: "Overnight", Default: true,
			Pools: []PoolRef{{Pool: "talk", Weight: 1}, {Pool: "music", Weight: 1}},
		}},
	}

	songs := []catalog.MusicTrack{}
	for index := 0; index < 20; index++ {
		songs = append(songs, track(
			"t"+string(rune('a'+index)), "Song", "Artist "+string(rune('a'+index)), 210))
	}

	return newStation(t, plan, []Source{podcastSource("pod1", "The Show", "p1"), musicSource("mus1", "House", "pl1")},
		&stubCatalog{
			episodes: map[string][]catalog.PodcastEpisode{"p1": {
				episode("new", "This morning's episode", published, 40),
				episode("archive", "One from the vault", now.AddDate(0, 0, -400), 40),
			}},
			playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		}, now)
}

// The overnight drop is HELD for the morning so it is not spent on an empty
// room — and nothing stopped the same block from filling the hour before the
// day starts with that show's back catalogue, so the hold delivered the new
// episode directly on top of a rerun of itself.
func TestTheRunUpToAHeldEpisodeIsNotFilledWithItsOwnShow(t *testing.T) {
	// 07:30, half an hour before the day opens. A 40-minute archive episode
	// started now would still be running at 08:10.
	justBefore := time.Date(2026, 9, 3, 7, 30, 0, 0, time.UTC)
	published := time.Date(2026, 9, 3, 4, 0, 0, 0, time.UTC)
	s := heldStation(t, justBefore, published)

	item, decision := s.decide()
	if item.ItemRef == "episode:archive" {
		t.Fatalf("the station filled the run-up to a held episode with that show's own back "+
			"catalogue, so the new episode lands on top of a rerun of itself:\n%s",
			decision.Explain())
	}
	if item.ItemRef == "episode:new" {
		t.Fatal("the held episode was spent before the listening day opened")
	}
}

// Scoped to the collision, not to the night. Five hours before the day opens,
// that show's back catalogue is perfectly good radio, and refusing it would
// empty the overnight schedule of exactly the shows that publish.
func TestAShowWithAHeldEpisodeStillPlaysEarlierInTheNight(t *testing.T) {
	earlier := time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC)
	published := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	s := heldStation(t, earlier, published)

	// Nothing about the archive episode should be refused at 02:00.
	_, decision := s.decide()
	for _, rejection := range decision.Rejected {
		if rejection.Ref == "episode:archive" {
			t.Fatalf("at 02:00, six hours before the day opens, the archive was refused: %s: %s",
				rejection.Rule, rejection.Reason)
		}
	}
}

// ---- the floor must not cost anything the listener notices ---------------

// anchoredEmptyStation has a booked show at 16:00, a general block whose pool
// has gone empty, and a music shelf full of long tracks that cannot fit whole
// into the gap in front of the appointment.
func anchoredEmptyStation(t *testing.T, now time.Time) *station {
	t.Helper()

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 0.5}, {ID: "music", Target: 0.5}},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"dead"}},
			{ID: "show", SourceIDs: []string{"booked"}},
		},
		Blocks: []Block{
			{ID: "general", Label: "General rotation", Default: true,
				Pools: []PoolRef{{Pool: "talk", Weight: 1}}},
			{
				ID: "show", Label: "All Things Considered",
				Enter: BlockEntry{At: "16:00", Days: "*", Hard: true, Start: StartMakeNext},
				Exit:  BlockExit{At: "17:00"},
				Pools: []PoolRef{{Pool: "show", Weight: 1}},
			},
		},
	}

	// Twenty-minute tracks: nothing fits a gap of a few minutes whole.
	songs := []catalog.MusicTrack{}
	for index := 0; index < 5; index++ {
		songs = append(songs, track(
			"t"+string(rune('a'+index)), "A long song", "Artist "+string(rune('a'+index)), 20*60))
	}

	return newStation(t, plan, []Source{
		podcastSource("dead", "A feed that stopped", "gone"),
		podcastSource("booked", "The booked show", "p-booked"),
		musicSource("mus1", "House", "pl1"),
	}, &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{
			"p-booked": {episode("atc", "All Things Considered", now.AddDate(0, 0, -1), 60)},
		},
		playlists: map[string][]catalog.MusicTrack{"pl1": songs},
	}, now)
}

// The appointment starts on its own second. The floor may fade an item out on
// the boundary — that is what keeps the boundary where the schedule put it —
// but it may never run over one.
func TestTheFloorNeverPushesABookedShowLate(t *testing.T) {
	// Four minutes before the booked hour, with nothing that fits it whole.
	justBefore := time.Date(2026, 9, 3, 15, 56, 0, 0, time.UTC)
	s := anchoredEmptyStation(t, justBefore)

	item, decision, err := s.tryDecide()
	if err != nil {
		t.Fatalf("silence in front of a booked show with a shelf full of music: %v (%s)",
			err, decision.Error)
	}
	gap := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC).Sub(justBefore)
	if item.MaxDuration <= 0 {
		t.Fatalf("a %s gap was filled with an unbounded item (%s, %ds) — it would run "+
			"straight over the booked show", gap, item.Title, item.DurationSeconds)
	}
	if item.MaxDuration > gap {
		t.Fatalf("the filler may run %s into a %s gap, so the booked show starts late",
			item.MaxDuration, gap)
	}
	// Cut on a boundary is only acceptable as a fade, never as a hard stop.
	if item.FadeOut <= 0 {
		t.Fatal("an item picked knowing the boundary would take it was not given a fade")
	}
}

// And below the smallest gap worth filling, the floor stands down rather than
// starting something it would have to fade almost immediately. A five-second
// stub of a song is a fault you can hear; an appointment opening five seconds
// early is not.
func TestTheFloorLeavesASliverAlone(t *testing.T) {
	sliver := time.Date(2026, 9, 3, 15, 59, 55, 0, time.UTC)
	s := anchoredEmptyStation(t, sliver)

	item, _, err := s.tryDecide()
	if err == nil && item.MaxDuration > 0 && item.MaxDuration < minBoundaryFill {
		t.Fatalf("the floor started a %s stub of %q to cover the last five seconds "+
			"before a booked show", item.MaxDuration, item.Title)
	}
}
