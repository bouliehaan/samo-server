package channels

import (
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Breaks: count and duration together, and what happens when the library
// cannot oblige.

// breakStation builds a station whose talk items are separated by a stopset of
// an ident, up to two spots, and one to three songs — about eight minutes.
func breakStation(t *testing.T, now time.Time, songs []catalog.MusicTrack, withSpots bool) *station {
	t.Helper()
	sources := []Source{
		podcastSource("pod1", "Talk One", "p1"),
		podcastSource("pod2", "Talk Two", "p2"),
		musicSource("mus1", "House", "pl1"),
	}
	pools := []Pool{
		{ID: "talk", SourceIDs: []string{"pod1", "pod2"}},
		{ID: "music", SourceIDs: []string{"mus1"}},
		{ID: "spots", SourceIDs: []string{}},
	}
	if withSpots {
		spots := musicSource("spots", "Spots", "plspots")
		spots.Role = RoleCommercial
		sources = append(sources, spots)
		pools[2].SourceIDs = []string{"spots"}
	}

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 0.75}, {ID: "music", Target: 0.25}},
		Pools:      pools,
		// The shape the brief describes: the programming is spoken word, and
		// music exists to go BETWEEN it. Music is in the break elements, not in
		// the block's rotation, which is what "a couple of songs between
		// podcasts" actually means.
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "talk"}},
			Breaks: &BreakPolicy{
				Between: []CategoryID{"talk"},
				Target:  BreakSize{Duration: "8m", Items: 2},
				Accept:  BreakRange{Duration: []string{"3m", "14m"}, Items: []int{1, 3}},
				Elements: []BreakElement{
					{Pool: "spots", Count: []int{0, 2}},
					{Pool: "music", Count: []int{1, 3}, Fill: true},
				},
			},
		}},
	}
	cat := &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{
			"p1": {
				episode("a1", "Talk one A", now.Add(-40*24*time.Hour), 30),
				episode("a2", "Talk one B", now.Add(-41*24*time.Hour), 30),
			},
			"p2": {
				episode("b1", "Talk two A", now.Add(-42*24*time.Hour), 30),
				episode("b2", "Talk two B", now.Add(-43*24*time.Hour), 30),
			},
		},
		playlists: map[string][]catalog.MusicTrack{
			"pl1": songs,
			"plspots": {
				track("s1", "Spot one", "Advertiser", 30),
				track("s2", "Spot two", "Advertiser", 30),
			},
		},
	}
	return newStation(t, plan, sources, cat, now)
}

func normalSongs() []catalog.MusicTrack {
	out := []catalog.MusicTrack{}
	for i := 0; i < 12; i++ {
		out = append(out, track(
			"t"+string(rune('a'+i)), "Song "+string(rune('A'+i)),
			"Artist "+string(rune('A'+i)), 230))
	}
	return out
}

// The case the brief calls out: with no commercial inventory at all, the break
// is still a break. No branch, no error, no silence — the elastic element takes
// up the slack.
func TestABreakWorksWithNoCommercials(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	s := breakStation(t, now, normalSongs(), false)

	first := s.play()
	if first.Category != "talk" {
		t.Fatalf("expected the station to open with talk, got %s", first.Category)
	}
	item, decision := s.decide()
	if decision.Break == nil {
		t.Fatalf("a break should follow the talk item\n%s", decision.Explain())
	}
	if item.Category != "music" {
		t.Fatalf("with no spots the break should be music, got %s", item.Category)
	}
	if !decision.Break.InRange {
		t.Fatalf("a music-only break should still land inside the accept range: %+v", decision.Break)
	}
	if decision.Break.Minutes < 3 || decision.Break.Minutes > 14 {
		t.Fatalf("break ran %dm, outside the 3–14m accept range", decision.Break.Minutes)
	}
}

// And with inventory, the spots appear — same policy, same code path.
func TestABreakUsesCommercialsWhenThereAreSome(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	s := breakStation(t, now, normalSongs(), true)

	s.play()
	_, decision := s.decide()
	if decision.Break == nil {
		t.Fatalf("a break should follow the talk item\n%s", decision.Explain())
	}
	spots := 0
	for _, title := range decision.Break.Items {
		if title == "Spot one" || title == "Spot two" {
			spots++
		}
	}
	if spots == 0 {
		t.Fatalf("with inventory available the break should contain a spot: %v", decision.Break.Items)
	}
}

// "Play two songs" is not a specification. With fifteen-minute songs, two of
// them is a half-hour break, so the planner takes one.
func TestABreakOfUnusuallyLongSongsTakesFewer(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	long := []catalog.MusicTrack{}
	for i := 0; i < 6; i++ {
		long = append(long, track(
			"L"+string(rune('a'+i)), "Long song "+string(rune('A'+i)),
			"Artist "+string(rune('A'+i)), 13*60))
	}
	s := breakStation(t, now, long, false)

	s.play()
	_, decision := s.decide()
	if decision.Break == nil {
		t.Fatalf("expected a break")
	}
	if len(decision.Break.Items) != 1 {
		t.Fatalf("two thirteen-minute songs is a twenty-six minute break; expected one, got %d (%v)",
			len(decision.Break.Items), decision.Break.Items)
	}
	if decision.Break.Minutes > 14 {
		t.Fatalf("break ran %dm, past the 14m ceiling", decision.Break.Minutes)
	}
}

// And with thirty-second songs, one is not a break at all, so it takes the
// most its count range allows.
func TestABreakOfUnusuallyShortSongsTakesMore(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	short := []catalog.MusicTrack{}
	for i := 0; i < 8; i++ {
		short = append(short, track(
			"S"+string(rune('a'+i)), "Short song "+string(rune('A'+i)),
			"Artist "+string(rune('A'+i)), 40))
	}
	s := breakStation(t, now, short, false)

	s.play()
	_, decision := s.decide()
	if decision.Break == nil {
		t.Fatalf("expected a break")
	}
	if len(decision.Break.Items) != 3 {
		t.Fatalf("forty-second songs should fill the count range; expected 3, got %d (%v)",
			len(decision.Break.Items), decision.Break.Items)
	}
	// Three forty-second songs is two minutes: short of the three-minute floor,
	// and the record has to admit that rather than pretend.
	if decision.Break.InRange {
		t.Fatalf("two minutes is below the 3m floor; the break should be reported as out of range")
	}
	if decision.Break.Note == "" {
		t.Fatalf("a compromise should say so: %+v", decision.Break)
	}
}

// A break plays as the unit it was planned as, in order, without the station
// re-deciding halfway through and wandering off.
func TestABreakPlaysAsAUnit(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	s := breakStation(t, now, normalSongs(), true)

	s.play()
	first := s.play()
	if first.Category == "talk" {
		t.Fatalf("expected the break to start, got talk")
	}
	if len(s.state.Queue) == 0 {
		t.Fatalf("the rest of the break should be queued")
	}
	planned := len(s.state.Queue) + 1

	for i := 1; i < planned; i++ {
		item := s.play()
		if item.Category == "talk" {
			t.Fatalf("the break was interrupted by programming at item %d", i+1)
		}
	}
	// And once it is over, programming resumes rather than another break.
	after := s.play()
	if after.Category != "talk" {
		t.Fatalf("after a break the station should return to programming, got %s", after.Category)
	}
}

// A break does not follow a break. Without that, the break's own last item and
// whatever follows are two things that want separating, and the station
// separates them for ever.
func TestBreaksDoNotChain(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	s := breakStation(t, now, normalSongs(), false)

	categories := []CategoryID{}
	for i := 0; i < 10; i++ {
		categories = append(categories, s.play().Category)
	}
	talk := 0
	for _, category := range categories {
		if category == "talk" {
			talk++
		}
	}
	if talk < 2 {
		t.Fatalf("the station played almost no programming in ten items — breaks are chaining: %v", categories)
	}
}
