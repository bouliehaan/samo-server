package channels

import (
	"context"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// From the real station, 2026-09-10. Matt and Shane's Secret Podcast — S tier,
// on a plan that surfaces S-tier episodes twice — published Ep 635 at 06:00.
// The station aired it at 08:08, to a Crosley in an empty house, and then
// never again: four A-tier first airings went out through the afternoon while
// the owed list showed the episode at the top, pending, credit 1 of 2.
//
// The decision records said itemSeparation for the first eight hours, which is
// the rule working. What they would have said after that — and did say all day
// for every other half-surfaced episode on the station — was
//
//	✕ #2551 - Daniel Kokotajlo    alreadyHeard: somebody here has already listened to this
//
// Nobody had. The station writes its own airings to the playback table under
// the reserved server account so that an episode it has been through does not
// come back as a rerun, and the already-heard gate read that row across every
// account, the station's included. So the station's first airing was also its
// last: a second surfacing existed only as a number in the plan, and the only
// way one ever went out was when a booked show was close enough that every rule
// had to be given up at once.

// stationEars is the playback table as the engine sees it: what people have
// heard, and what the station has aired, kept apart.
type stationEars struct {
	people  map[string]EpisodeProgress
	station map[string]EpisodeProgress
}

func newStationEars() *stationEars {
	return &stationEars{people: map[string]EpisodeProgress{}, station: map[string]EpisodeProgress{}}
}

func (e *stationEars) EpisodeProgress(_ context.Context, ids []string) (map[string]EpisodeListening, error) {
	out := map[string]EpisodeListening{}
	for _, id := range ids {
		entry, any := EpisodeListening{}, false
		if state, ok := e.people[id]; ok {
			entry.Listener, any = state, true
		}
		if state, ok := e.station[id]; ok {
			entry.Station, any = state, true
		}
		if any {
			out[id] = entry
		}
	}
	return out, nil
}

// aired is what recordAiring writes once an episode has gone out in full.
func (e *stationEars) aired(episodeID string, seconds int) {
	e.station[episodeID] = EpisodeProgress{Completed: true, ProgressSeconds: seconds}
}

// heardBySomeone is a person finishing the episode in their own client.
func (e *stationEars) heardBySomeone(episodeID string, seconds int) {
	e.people[episodeID] = EpisodeProgress{Completed: true, ProgressSeconds: seconds}
}

// secondSurfacingStation is the shape of the 2026-09-10 report: one S-tier show
// with today's episode, one A-tier show with today's episode, music between,
// and a plan that wants both tiers surfaced twice.
func secondSurfacingStation(t *testing.T, now time.Time, ears *stationEars) *station {
	t.Helper()

	plan := Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		ListeningDay: &DaySpec{Start: "08:00", End: "23:00"},
		Freshness:    FreshnessPolicy{Surfacings: map[string]int{"S": 2, "A": 2}},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"mssp", "tetra"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
		},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "talk", Weight: 1}, {Pool: "music", Weight: 1}},
		}},
	}

	songs := []catalog.MusicTrack{}
	for index := 0; index < 30; index++ {
		songs = append(songs, track(
			"t"+string(rune('a'+index)), "Song", "Artist "+string(rune('a'+index)), 210))
	}

	mssp := podcastSource("mssp", "Matt and Shane's Secret Podcast", "p-mssp")
	mssp.Config["tier"] = "S"
	tetra := podcastSource("tetra", "Tetragrammaton with Rick Rubin", "p-tetra")
	tetra.Config["tier"] = "A"

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	s := newStation(t, plan, []Source{mssp, tetra, musicSource("mus1", "House", "pl1")},
		&stubCatalog{
			episodes: map[string][]catalog.PodcastEpisode{
				"p-mssp": {
					episode("ep635", "Ep 635 - Podcaster's Union", today.Add(6*time.Hour), 59),
					episode("ep634", "Ep 634 - Department of Jokes", today.AddDate(0, 0, -6), 76),
				},
				"p-tetra": {
					episode("tetra200", "Episode 200: Max Richter", today.Add(-17*time.Hour), 111),
					episode("tetra199", "Episode 199", today.AddDate(0, 0, -8), 100),
				},
			},
			playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		}, now)
	s.engine.Listened = ears
	return s
}

// airedInFull plays an episode from the top exactly as the live station did at
// 08:08: the play log, the obligation's credit, and the station's own playback
// row all say it went out, and the clock moves past it.
func (s *station) airedInFull(ears *stationEars, sourceID, episodeID string, length time.Duration) {
	s.t.Helper()
	ref := "episode:" + episodeID
	s.history.Record(MemoryPlay{
		SourceID: sourceID, ItemRef: ref, Category: "talk",
		StartedAt: s.now, EndedAt: s.now.Add(length),
		DurationSeconds: int(length / time.Second),
	})
	if err := s.engine.Obligations.Credit(context.Background(), ref, 1, s.now.Add(length)); err != nil {
		s.t.Fatalf("credit: %v", err)
	}
	ears.aired(episodeID, int(length/time.Second))
	s.now = s.now.Add(length)
}

// rejectionOf is what the rules said about a ref, or "" if it was not refused.
func rejectionOf(decision Decision, ref string) (string, string) {
	for _, rejection := range decision.Rejected {
		if rejection.Ref == ref {
			return rejection.Rule, rejection.Reason
		}
	}
	return "", ""
}

// THE COMPLAINT. An S-tier episode owed two surfacings, aired once by the
// station, is offered again once separation has run its course — the station's
// own record of the first airing is not a person having heard it.
func TestAnEpisodeTheStationAiredOnceIsStillOwedItsSecondSurfacing(t *testing.T) {
	ears := newStationEars()
	now := time.Date(2026, 9, 10, 8, 8, 0, 0, time.UTC)
	s := secondSurfacingStation(t, now, ears)
	// Make the obligation exist the way the live one did: noticed, then aired.
	s.env()
	s.airedInFull(ears, "mssp", "ep635", 59*time.Minute)

	if queue := s.env().owed; !queue.Owes("episode:ep635") {
		t.Fatal("one airing of two settled the obligation; the fixture is not testing a second surfacing")
	}

	// Well past the eight-hour item separation, with the afternoon's A-tier
	// first airing behind it too.
	s.now = time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	item, decision := s.decide()
	if rule, reason := rejectionOf(decision, "episode:ep635"); rule != "" {
		t.Fatalf("the station refused the episode it still owes: %s: %s\n%s", rule, reason, decision.Explain())
	}
	if item.ItemRef != "episode:ep635" {
		t.Fatalf("with an S-tier second surfacing owed the station played %q\n%s", item.Title, decision.Explain())
	}
}

// A person having heard it is a different matter entirely, and still keeps the
// episode off the air however much of it the station has yet to surface.
func TestAPersonHavingHeardItStillKeepsItOffTheAir(t *testing.T) {
	ears := newStationEars()
	now := time.Date(2026, 9, 10, 8, 8, 0, 0, time.UTC)
	s := secondSurfacingStation(t, now, ears)
	s.env()
	s.airedInFull(ears, "mssp", "ep635", 59*time.Minute)
	// Listened to on a phone, later in the day.
	ears.heardBySomeone("ep635", 59*60)

	s.now = time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	item, decision := s.decide()
	if rule, _ := rejectionOf(decision, "episode:ep635"); rule != "alreadyHeard" {
		t.Fatalf("an episode somebody has heard was refused for %q, want alreadyHeard\n%s", rule, decision.Explain())
	}
	if item.ItemRef == "episode:ep635" {
		t.Fatalf("the station re-aired an episode somebody has already heard\n%s", decision.Explain())
	}
}

// And once the station has surfaced an episode as many times as it owes, its
// own record keeps it from coming round again as back catalogue — which is what
// the station's own playback row was written for in the first place.
func TestTheStationsOwnRecordStillKeepsASettledEpisodeOffTheAir(t *testing.T) {
	ears := newStationEars()
	now := time.Date(2026, 9, 10, 8, 8, 0, 0, time.UTC)
	s := secondSurfacingStation(t, now, ears)
	s.env()
	s.airedInFull(ears, "mssp", "ep635", 59*time.Minute)
	s.now = time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	s.airedInFull(ears, "mssp", "ep635", 59*time.Minute)
	if queue := s.env().owed; queue.Owes("episode:ep635") {
		t.Fatal("two airings of two left the obligation pending")
	}

	// A whole listening day later, with every clock-based rule long expired.
	s.now = time.Date(2026, 9, 11, 19, 0, 0, 0, time.UTC)
	item, decision := s.decide()
	if item.ItemRef == "episode:ep635" {
		t.Fatalf("a settled episode came back as a rerun\n%s", decision.Explain())
	}
	if rule, _ := rejectionOf(decision, "episode:ep635"); rule != "alreadyHeard" {
		t.Fatalf("a settled, twice-aired episode was refused for %q, want alreadyHeard\n%s", rule, decision.Explain())
	}
}

// An episode heard on a phone has reached the listener, so the station stops
// owing it. Left pending it would head the owed list until its window ran out
// while the already-heard rule refused it at every turn — and a block that
// hands over when obligations.pending reaches zero would never hand over.
func TestAnEpisodeSomebodyHeardElsewhereIsNoLongerOwed(t *testing.T) {
	ears := newStationEars()
	now := time.Date(2026, 9, 10, 8, 8, 0, 0, time.UTC)
	s := secondSurfacingStation(t, now, ears)
	s.env()
	s.airedInFull(ears, "mssp", "ep635", 59*time.Minute)
	if !s.env().owed.Owes("episode:ep635") {
		t.Fatal("the fixture should still owe a second surfacing at this point")
	}

	ears.heardBySomeone("ep635", 59*60)
	s.now = time.Date(2026, 9, 10, 17, 20, 0, 0, time.UTC)
	if queue := s.env().owed; queue.Owes("episode:ep635") {
		t.Fatalf("an episode somebody has heard is still owed: %+v", queue.Pending)
	}
	stored, err := s.engine.Obligations.List(context.Background(), s.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, obligation := range stored {
		if obligation.ItemRef != "episode:ep635" {
			continue
		}
		if obligation.State != ObligationSatisfied || obligation.Credit < obligation.Target() {
			t.Fatalf("the stored row was not settled: %+v", obligation)
		}
		return
	}
	t.Fatal("the obligation vanished instead of settling")
}

// The station's own airing is not that route. Only a person settles an
// obligation this way; the radio's listening is the credit the row carries.
func TestTheStationsOwnAiringDoesNotSettleWhatItStillOwes(t *testing.T) {
	ears := newStationEars()
	now := time.Date(2026, 9, 10, 8, 8, 0, 0, time.UTC)
	s := secondSurfacingStation(t, now, ears)
	s.env()
	s.airedInFull(ears, "mssp", "ep635", 59*time.Minute)

	s.now = time.Date(2026, 9, 10, 17, 20, 0, 0, time.UTC)
	if queue := s.env().owed; !queue.Owes("episode:ep635") {
		t.Fatal("one airing of two, and the station's own record of it, settled the obligation")
	}
}
