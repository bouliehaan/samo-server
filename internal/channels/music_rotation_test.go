package channels

import (
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The music rotation, measured rather than argued about.
//
// "137 of my 300 songs are Elvis and I never hear Elvis, but I hear the same
// four records all day" is a claim about a distribution, so these tests run a
// day of radio and count what came out.

// elvisPlaylist is the real shape of the complaint: a third of the collection is
// one artist, the rest is a long tail of one- and two-track artists.
//
// Elvis sits at the END of the playlist on purpose. That is where a block of one
// artist lands when you add it to a playlist you already had, and it is the
// arrangement that shows whether position decides what can air.
func elvisPlaylist(elvis, others int) []catalog.MusicTrack {
	tracks := make([]catalog.MusicTrack, 0, elvis+others)
	for i := 0; i < others; i++ {
		tracks = append(tracks, track(
			"other-"+strconv.Itoa(i),
			"Song "+strconv.Itoa(i),
			"Artist "+strconv.Itoa(i%120),
			200+(i%5)*20,
		))
	}
	for i := 0; i < elvis; i++ {
		tracks = append(tracks, track(
			"elvis-"+strconv.Itoa(i),
			"Elvis "+strconv.Itoa(i),
			"Elvis Presley",
			160+(i%7)*15,
		))
	}
	return tracks
}

// musicOnlyPlan is a station that plays nothing but the one playlist, so the
// only thing deciding the running order is the music rotation itself.
func musicOnlyPlan() Plan {
	return Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "music", Label: "Music", Target: 1}},
		Pools:      []Pool{{ID: "music", SourceIDs: []string{"mus1"}}},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "music", Weight: 1}},
		}},
	}
}

// spin plays `count` items and reports what went out, by artist and by track.
func spin(t *testing.T, s *station, count int) (map[string]int, map[string]int) {
	t.Helper()
	byArtist := map[string]int{}
	byTrack := map[string]int{}
	for i := 0; i < count; i++ {
		item := s.play()
		byArtist[item.Artist]++
		byTrack[item.ItemRef]++
	}
	return byArtist, byTrack
}

// Everything in the playlist has to be reachable.
//
// enumeratePlaylist used to take the first searchDepth tracks, and
// MusicTracksForPlaylist returns them in playlist ORDER, so with the default
// depth of 200 a 300-track playlist had a hundred songs that were not merely
// unlikely — never enumerated, never scored, never even rejected. Which hundred
// was decided by where they happened to sit in the list.
//
// A decision still only looks at searchDepth of them, which is what that knob is
// for. The claim is about the union over time: the window rotates, so every song
// comes into view, and none of them depends on its position to exist.
func TestEveryTrackInAPlaylistCanAir(t *testing.T) {
	start := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	sources := []Source{musicSource("mus1", "House Playlist", "pl1")}
	cat := &stubCatalog{playlists: map[string][]catalog.MusicTrack{
		"pl1": elvisPlaylist(137, 163),
	}}

	seen := map[string]bool{}
	// One full turn is len(playlist) minutes; sample across a little more.
	for minute := 0; minute < 330; minute += 15 {
		s := newStation(t, musicOnlyPlan(), sources, cat, start.Add(time.Duration(minute)*time.Minute))
		for _, candidate := range s.candidates() {
			seen[candidate.Ref] = true
		}
	}
	if len(seen) != 300 {
		t.Fatalf("over a full rotation only %d of 300 songs were ever offered — %d can never air",
			len(seen), 300-len(seen))
	}
}

// Half the playlist should be about half the airtime.
//
// What you put on the shelf is the instruction. An artist who is 46% of the
// collection turning up in 3% of the hour is not a rotation exercising
// judgement, it is a rule overruling the one statement of taste the operator
// actually made — and the answer is not "less wrong", it is their share.
//
// Which means accepting two of theirs back to back. At this concentration the
// gap the arithmetic asks for is shorter than one record, so anything that
// always puts another artist in between caps them at p/(1+p) — 31% for a 46%
// artist — no matter how much the window is loosened.
func TestTheDominantArtistGetsHeard(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	sources := []Source{musicSource("mus1", "House Playlist", "pl1")}
	cat := &stubCatalog{playlists: map[string][]catalog.MusicTrack{
		"pl1": elvisPlaylist(137, 163),
	}}
	s := newStation(t, musicOnlyPlan(), sources, cat, now)

	byArtist, _ := spin(t, s, 200)
	elvis := byArtist["Elvis Presley"]
	share := float64(elvis) / 200.0

	const catalogue = 137.0 / 300.0
	t.Logf("Elvis aired %d of 200 (%.0f%%); catalogue share is %.0f%%",
		elvis, share*100, catalogue*100)
	// A band, not a target — the pick is still weighted and the hour is never
	// an exact mirror of the shelf. It has to be recognisably the same number.
	if share < catalogue-0.08 || share > catalogue+0.08 {
		t.Fatalf("Elvis is %.0f%% of the playlist and got %.0f%% of the airtime (%d of 200)",
			catalogue*100, share*100, elvis)
	}
}

// A playlist is not spaced by artist at all.
//
// Two records by one artist back to back is a shelf that is half that artist
// showing through, not the rotation slipping — and it is the only way their
// share of the hour can match their share of the shelf, because any spacing at
// all forces another artist in between and caps them at p/(1+p).
func TestAPlaylistIsNotSpacedByArtist(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	sources := []Source{musicSource("mus1", "House Playlist", "pl1")}
	cat := &stubCatalog{playlists: map[string][]catalog.MusicTrack{
		"pl1": elvisPlaylist(137, 163),
	}}
	s := newStation(t, musicOnlyPlan(), sources, cat, now)

	for _, candidate := range s.candidates() {
		if candidate.Traits.HasCreator || candidate.Creator != "" {
			t.Fatalf("a playlist track is still carrying a creator to be separated on: %+v", candidate.Creator)
		}
	}

	// Back to back does happen, and is not a defect.
	last, backToBack := "", 0
	for i := 0; i < 200; i++ {
		item := s.play()
		if item.Artist != "" && item.Artist == last {
			backToBack++
		}
		last = item.Artist
	}
	if backToBack == 0 {
		t.Fatal("46% of the shelf is one artist and he never once followed himself — something is still spacing him")
	}
	t.Logf("same artist twice in a row: %d times in 200", backToBack)
}

// The queue: a record does not come round again until nearly the whole playlist
// has been between, and the order is different every pass.
//
// Not the WHOLE playlist, deliberately. A window that covers every last track
// releases them one at a time in the order they were played, so the second pass
// is a note-for-note replay of the first — a loop, not a shuffle. A tail stays
// in hand so there is a real choice at every pick, which is the same reason a
// dealt shuffle bag is at its worst on the last card.
func TestAPlaylistPlaysAsAQueue(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	sources := []Source{musicSource("mus1", "House Playlist", "pl1")}
	cat := &stubCatalog{playlists: map[string][]catalog.MusicTrack{
		"pl1": elvisPlaylist(137, 163),
	}}
	s := newStation(t, musicOnlyPlan(), sources, cat, now)

	order := make([]string, 0, 600)
	for i := 0; i < 600; i++ {
		order = append(order, s.play().ItemRef)
	}

	// Every record gets played.
	distinct := map[string]bool{}
	for _, ref := range order {
		distinct[ref] = true
	}
	if len(distinct) != 300 {
		t.Fatalf("600 picks used only %d of the 300 records", len(distinct))
	}

	// And none comes back until nearly all the others have been.
	lastAt := map[string]int{}
	closest := len(order)
	for index, ref := range order {
		if prev, seen := lastAt[ref]; seen && index-prev-1 < closest {
			closest = index - prev - 1
		}
		lastAt[ref] = index
	}
	if closest < 250 {
		t.Fatalf("a record came back with only %d others between it, out of a 300-song playlist", closest)
	}
	t.Logf("closest repeat had %d other records between", closest)

	// The second pass is not the first pass again.
	same := 0
	for i := 0; i < 300; i++ {
		if order[i] == order[i+300] {
			same++
		}
	}
	if same > 30 {
		t.Fatalf("%d of 300 slots played the same record on both passes — that is a loop, not a shuffle", same)
	}
	t.Logf("slots identical across the two passes: %d of 300", same)
}

// A short playlist must not be held hostage by a window written for a long one.
//
// Eight hours of item separation on a twenty-track playlist means nineteen
// records are unplayable all afternoon, and the station spends the day relaxing
// its own rules to get anything at all out of a playlist it was handed.
func TestAShortPlaylistStillTurnsOver(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	sources := []Source{musicSource("mus1", "Small", "pl1")}
	cat := &stubCatalog{playlists: map[string][]catalog.MusicTrack{
		"pl1": elvisPlaylist(0, 20),
	}}
	s := newStation(t, musicOnlyPlan(), sources, cat, now)

	start := s.now
	distinct := map[string]bool{}
	for i := 0; i < 20; i++ {
		item, decision := s.step()
		if len(decision.Relaxed) > 0 {
			t.Fatalf("pick %d relaxed %v to get through a twenty-track playlist\n%s",
				i, decision.Relaxed, decision.Explain())
		}
		distinct[item.ItemRef] = true
	}
	elapsed := s.now.Sub(start)
	if len(distinct) < 18 {
		t.Fatalf("twenty picks from a twenty-track playlist used %d records", len(distinct))
	}
	if elapsed > 3*time.Hour {
		t.Fatalf("a twenty-track playlist took %s to get through", elapsed)
	}
	t.Logf("%d distinct in twenty picks, over %s", len(distinct), elapsed.Round(time.Minute))
}

// No song may be on heavy rotation while hundreds sit unplayed.
//
// The complaint that named four specific records is this one: with a library
// this size, two hundred picks should be two hundred different songs long before
// anything comes round a fourth time.
func TestNoHandfulOfSongsDominates(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	sources := []Source{musicSource("mus1", "House Playlist", "pl1")}
	cat := &stubCatalog{playlists: map[string][]catalog.MusicTrack{
		"pl1": elvisPlaylist(137, 163),
	}}
	s := newStation(t, musicOnlyPlan(), sources, cat, now)

	_, byTrack := spin(t, s, 200)

	type row struct {
		ref   string
		plays int
	}
	rows := make([]row, 0, len(byTrack))
	for ref, plays := range byTrack {
		rows = append(rows, row{ref, plays})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].plays > rows[j].plays })

	top := rows[:min(5, len(rows))]
	topPlays := 0
	for _, r := range top {
		topPlays += r.plays
	}
	t.Logf("%d distinct songs in 200 picks; top five: %v", len(rows), top)

	if len(rows) < 100 {
		t.Fatalf("200 picks from a 300-song playlist used only %d distinct songs", len(rows))
	}
	if share := float64(topPlays) / 200.0; share > 0.15 {
		t.Fatalf("five songs took %.0f%% of a 200-song stretch: %v", share*100, top)
	}
}
