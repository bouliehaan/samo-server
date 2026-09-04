package channels

import (
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// From the real station, 2026-09-03 09:12. The station played an A-tier bonus
// episode and passed over two S-tier ones, and the decision record said why:
//
//	✕ RealDGC LIVE 009            sourceSeparation: this show aired 0s ago, needs 45m0s apart
//	✕ Grow Room Upgrades…         sourceSeparation: this show aired 0s ago, needs 45m0s apart
//
// while the same two episodes sat at the top of the owed queue with credit 0%.
// Both statements came from one airing, and they cannot both be true: either
// the listener heard that show a moment ago, or the station still owes it to
// them. Credit 0% is the station's own judgement that nobody heard it — an
// airing that began before the listening day opened, worth no exposure.
//
// The tiers were never the problem. The S-tier episodes were vetoed by a hard
// rule before scoring ever saw them; had either survived it would have scored
// freshness 4.00 against the A-tier winner's 3.40 and taken the slot easily.
//
// itemSeparation and airingCap already discount an airing nobody heard — both
// say so at length in their own comments. The source, creator and family
// windows could not, because the play log never wrote down what an airing was
// worth. Now it does.

// fullyHeard is an airing that reached the listener, which is what every
// timestamp in the play log meant before exposure was recorded alongside it.
func fullyHeard(at time.Time) lastAiring { return lastAiring{At: at, Exposure: 1} }

// unheardStation is the shape of the 09:12 report: one S-tier show with a new
// episode waiting, one A-tier show with a new episode waiting, and music to
// fall back on. The listening day opens at 08:00.
func unheardStation(t *testing.T, now time.Time) *station {
	t.Helper()

	plan := Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		ListeningDay: &DaySpec{Start: "08:00", End: "23:00"},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"dgc", "bonus"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
		},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "talk", Weight: 1}, {Pool: "music", Weight: 1}},
		}},
	}

	songs := []catalog.MusicTrack{}
	for index := 0; index < 20; index++ {
		songs = append(songs, track(
			"t"+string(rune('a'+index)), "Song", "Artist "+string(rune('a'+index)), 210))
	}

	dgc := podcastSource("dgc", "The Dude Grows Show", "p-dgc")
	dgc.Config["tier"] = "S"
	bonus := podcastSource("bonus", "Bonus Feed", "p-bonus")
	bonus.Config["tier"] = "A"

	return newStation(t, plan, []Source{dgc, bonus, musicSource("mus1", "House", "pl1")},
		&stubCatalog{
			episodes: map[string][]catalog.PodcastEpisode{
				// The one the station owes: published yesterday evening, never
				// surfaced. Short enough that nothing rations it.
				"p-dgc": {
					episode("new-dgc", "Grow Room Upgrades", now.Add(-14*time.Hour), 40),
					episode("old-dgc", "An older grow room episode", now.AddDate(0, 0, -40), 40),
				},
				"p-bonus": {episode("new-bonus", "Rats! (Bonus Episode)", now.Add(-8*time.Hour), 40)},
			},
			playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		}, now)
}

// airedIntoAnEmptyRoom is the airing at the heart of the report: it started
// before the listening day opened, so the block rated it worth nothing, and it
// finished at the very moment this decision is being made.
//
// Balanced against an equal stretch from the other show earlier in the same
// window, which is not decoration. The balance horizon is six hours; leave this
// airing as the only thing in it and one show is 100% of the station's recent
// output, which swings sourceDeficit by more than a whole tier step and decides
// the test on an artefact of the fixture rather than on the rule under test.
// The real station had 270 hours of history behind this decision.
func airedIntoAnEmptyRoom(s *station, now time.Time, exposure float64) {
	const length = 2*time.Hour + 12*time.Minute

	heard := 1.0
	earlier := now.Add(-6 * time.Hour)
	s.history.Record(MemoryPlay{
		SourceID: "bonus", ItemRef: "episode:old-bonus", Category: "talk",
		StartedAt: earlier, EndedAt: earlier.Add(length),
		DurationSeconds: int(length / time.Second),
		Exposure:        &heard,
	})

	started := now.Add(-length)
	s.history.Record(MemoryPlay{
		SourceID: "dgc", ItemRef: "episode:old-dgc", Category: "talk",
		StartedAt: started, EndedAt: now,
		DurationSeconds: int(length / time.Second),
		Exposure:        &exposure,
	})
}

// separationRejection is what a separation rule said about a ref, or "" if none
// of them had anything to say.
func separationRejection(decision Decision, ref string) (string, string) {
	for _, rejection := range decision.Rejected {
		if rejection.Ref != ref {
			continue
		}
		switch rejection.Rule {
		case "sourceSeparation", "creatorSeparation", "familySeparation":
			return rejection.Rule, rejection.Reason
		}
	}
	return "", ""
}

// THE COMPLAINT. An airing worth no exposure must not hold back the show it
// belongs to — least of all the episode the station is still telling you it
// owes you.
func TestAnAiringNobodyHeardDoesNotBlockTheShowItOwes(t *testing.T) {
	morning := time.Date(2026, 9, 3, 9, 12, 0, 0, time.UTC)
	s := unheardStation(t, morning)
	airedIntoAnEmptyRoom(s, morning, 0)

	item, decision := s.decide()

	if rule, reason := separationRejection(decision, "episode:new-dgc"); rule != "" {
		t.Fatalf("the S-tier episode was held by %s (%q) on the strength of an airing "+
			"the station itself scored at zero exposure — the same airing that left it "+
			"owed with credit 0%%", rule, reason)
	}
	if item.ItemRef != "episode:new-dgc" {
		t.Fatalf("the station played %s instead of the S-tier episode it owed; rejections: %s",
			item.ItemRef, rejectionsFor(decision, "dgc"))
	}
}

// The other half, and the reason this is a scaling rather than an exemption: an
// airing that DID reach the listener must still separate the show. Switching
// the rule off for everything would be a different bug with the same shape.
func TestAnAiringThatReachedYouStillBlocksTheShow(t *testing.T) {
	morning := time.Date(2026, 9, 3, 9, 12, 0, 0, time.UTC)
	s := unheardStation(t, morning)
	airedIntoAnEmptyRoom(s, morning, 1)

	item, decision := s.decide()

	if rule, _ := separationRejection(decision, "episode:new-dgc"); rule == "" {
		t.Fatalf("a show heard in full a moment ago was not separated at all; "+
			"the station played %s with rejections: %s",
			item.ItemRef, rejectionsFor(decision, "dgc"))
	}
	if item.ItemRef == "episode:new-dgc" {
		t.Fatal("two episodes of the same show back to back, right after a full airing")
	}
}

// Half an airing is half a rule — and only for what the station owes.
//
// Tested against separationFor + sinceOK rather than through a decision,
// because the end-to-end window is not the configured one:
// fitSeparationToLibrary shrinks it to what the library can actually satisfy,
// so a test asserting minutes through the engine would be measuring the
// fitting, not the scaling.
func TestSeparationScalesWithHowMuchOfTheAiringLanded(t *testing.T) {
	now := time.Date(2026, 9, 3, 9, 12, 0, 0, time.UTC)
	const window = 45 * time.Minute

	cases := []struct {
		name     string
		owed     bool
		exposure float64
		ago      time.Duration
		ok       bool
	}{
		// What the station owes you: the window scales.
		{"owed, nobody heard the airing", true, 0, time.Second, true},
		{"owed, heard in full, inside the window", true, 1, 30 * time.Minute, false},
		{"owed, heard in full, past the window", true, 1, 46 * time.Minute, true},
		{"owed, half heard, inside the halved window", true, 0.5, 10 * time.Minute, false},
		{"owed, half heard, past the halved window", true, 0.5, 30 * time.Minute, true},
		{"owed, barely heard, past its sliver", true, 0.1, 5 * time.Minute, true},

		// Ordinary programming: the running order is a fact about the station,
		// not about who was awake, so the full window always applies.
		{"back catalogue, nobody heard the airing", false, 0, time.Second, false},
		{"back catalogue, half heard, past the halved window", false, 0.5, 30 * time.Minute, false},
		{"back catalogue, past the full window", false, 0, 46 * time.Minute, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			airing := lastAiring{At: now.Add(-tc.ago), Exposure: tc.exposure}
			candidate := Candidate{Ref: "episode:x", Owed: tc.owed}
			ok, reason := sinceOK(airing, now, separationFor(candidate, airing, window), "this show")
			if ok != tc.ok {
				t.Fatalf("owed=%v exposure %v, aired %s ago against a %s window: ok=%v (want %v) %q",
					tc.owed, tc.exposure, tc.ago, window, ok, tc.ok, reason)
			}
		})
	}
}

// An airing with no recorded exposure has to bind in full. Every row written
// before the column existed reads as zero in Go and as 1 in the database, and
// getting that backwards would switch separation off for the entire history.
func TestAnAiringWithNoRecordedExposureStillSeparates(t *testing.T) {
	now := time.Date(2026, 9, 3, 9, 12, 0, 0, time.UTC)

	play := MemoryPlay{SourceID: "dgc", StartedAt: now.Add(-time.Hour), EndedAt: now}
	if got := play.exposure(); got != 1 {
		t.Fatalf("a play that says nothing about exposure resolved to %v, not a full airing", got)
	}

	if ok, _ := sinceOK(fullyHeard(now.Add(-time.Minute)), now, 45*time.Minute, "this show"); ok {
		t.Fatal("an airing a minute ago was not separated at all")
	}
}
