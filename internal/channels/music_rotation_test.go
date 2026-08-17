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

// mixedPlaylist is n interchangeable tracks of ordinary, varied lengths.
//
// Deliberately not elvisPlaylist: the question here is what happens BETWEEN
// playlists, and one artist dominating inside one of them would only make the
// numbers harder to read.
func mixedPlaylist(prefix string, n int) []catalog.MusicTrack {
	tracks := make([]catalog.MusicTrack, 0, n)
	for i := 0; i < n; i++ {
		tracks = append(tracks, track(
			prefix+"-"+strconv.Itoa(i),
			"Song "+strconv.Itoa(i),
			"Artist "+strconv.Itoa(i%90),
			140+(i*37)%280, // 2:20 to 7:00
		))
	}
	return tracks
}

// manyPlaylistStation is what a personal channel looks like when somebody ticks
// every box in the playlist picker: five bags of wildly different sizes.
func manyPlaylistStation(t *testing.T) (*station, []string, map[string]int) {
	t.Helper()
	sizes := map[string]int{"pl1": 400, "pl2": 150, "pl3": 60, "pl4": 25, "pl5": 12}
	names := []string{"pl1", "pl2", "pl3", "pl4", "pl5"}
	playlists := map[string][]catalog.MusicTrack{}
	sources := make([]Source, 0, len(names))
	ids := make([]string, 0, len(names))
	for index, name := range names {
		playlists[name] = mixedPlaylist(name, sizes[name])
		id := "mus" + strconv.Itoa(index+1)
		ids = append(ids, id)
		sources = append(sources, musicSource(id, name, name))
	}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "music", Label: "Music", Target: 1}},
		Pools:      []Pool{{ID: "music", SourceIDs: ids}},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "music", Weight: 1}},
		}},
	}
	bySize := map[string]int{}
	for index, name := range names {
		bySize[ids[index]] = sizes[name]
	}
	return newStation(t, plan, sources, &stubCatalog{playlists: playlists},
		time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)), ids, bySize
}

// Adding a second playlist must not turn the first one into background music.
//
// Every source used to get the same slice of its category, which is the right
// answer for two podcasts and a badly wrong one for two playlists. A show with
// five hundred back episodes does not deserve more of the week than one with
// twenty; a shelf with four hundred records absolutely does deserve more of the
// hour than a shelf with twelve. Equal airtime per SOURCE means airtime per
// RECORD inversely proportional to how much you put in — so the twelve-track
// playlist took a fifth of the day and each of its songs came round twelve
// times while four hundred records waited for their first spin.
//
// Nothing about the rotation looked broken while that happened, which is what
// made it hard to see: each playlist still ran a clean queue, no song repeated
// inside its own turn, and every decision still justified itself. The twelve
// were simply being asked to fill a share they were far too small for, all day.
func TestASmallPlaylistDoesNotDrownOutABigOne(t *testing.T) {
	s, ids, size := manyPlaylistStation(t)

	total := 0
	for _, count := range size {
		total += count
	}
	plays := map[string]int{}
	const picks = 1200
	for i := 0; i < picks; i++ {
		plays[s.play().SourceID]++
	}

	perTrack := map[string]float64{}
	for _, id := range ids {
		perTrack[id] = float64(plays[id]) / float64(size[id])
		t.Logf("%s: %3d tracks (%.1f%% of the library) took %.1f%% of the airtime — %.2f plays per track",
			id, size[id], float64(size[id])/float64(total)*100,
			float64(plays[id])/picks*100, perTrack[id])
	}

	// The claim, stated as a ratio so it does not depend on the exact sizes: a
	// record on the small shelf may not be heard several times over for every
	// once a record on the big shelf is heard. Not equality — a small bag holds
	// a larger fraction of itself in hand so there is something to choose
	// between (minQueueChoices), so it does turn over a little faster.
	least, most := perTrack[ids[0]], perTrack[ids[0]]
	for _, id := range ids {
		if perTrack[id] < least {
			least = perTrack[id]
		}
		if perTrack[id] > most {
			most = perTrack[id]
		}
	}
	if least <= 0 {
		t.Fatalf("a whole playlist never aired: %v", perTrack)
	}
	if spread := most / least; spread > 3 {
		t.Fatalf("songs on the smallest playlist are heard %.1fx as often as songs on the biggest one", spread)
	}
}

// A playlist's share of the hour is its share of the shelf.
//
// The ratio test above catches the disaster; this one states the intent. Shares
// are measured in RUNNING TIME rather than track count, so twenty ten-minute
// pieces and fifty four-minute ones have the same claim on the day — which is
// why the band has to be generous enough to absorb the difference between a
// playlist's track share and its runtime share.
func TestAPlaylistGetsItsShareOfTheShelf(t *testing.T) {
	s, ids, size := manyPlaylistStation(t)

	total := 0
	for _, count := range size {
		total += count
	}
	plays := map[string]int{}
	const picks = 1200
	for i := 0; i < picks; i++ {
		plays[s.play().SourceID]++
	}

	for _, id := range ids {
		shelf := float64(size[id]) / float64(total)
		air := float64(plays[id]) / picks
		if air < shelf/2 || air > shelf*2 {
			t.Fatalf("%s is %.1f%% of the library and took %.1f%% of the airtime",
				id, shelf*100, air*100)
		}
	}
}

// Depth decides the split between BAGS and nothing else.
//
// A podcast is a strand, not a bag: you subscribed to the show, and an archive
// of five hundred episodes is not a reason to hear that show more often than one
// with twenty. Only sources whose items are interchangeable get weighted by how
// many of them there are, and an explicit weight still means what it says.
func TestOnlyShuffledSourcesAreWeightedByDepth(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	sources := []Source{
		podcastSource("pod1", "Deep Archive", "p1"),
		podcastSource("pod2", "New Show", "p2"),
		musicSource("mus1", "Big", "pl1"),
		musicSource("mus2", "Small", "pl2"),
	}
	deep := make([]catalog.PodcastEpisode, 0, 200)
	for i := 0; i < 200; i++ {
		deep = append(deep, episode("d"+strconv.Itoa(i), "Deep "+strconv.Itoa(i),
			now.Add(-time.Duration(i)*24*time.Hour), 40))
	}
	cat := &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{
			"p1": deep,
			"p2": {episode("n1", "New 1", now.Add(-24*time.Hour), 40)},
		},
		playlists: map[string][]catalog.MusicTrack{
			"pl1": mixedPlaylist("big", 300),
			"pl2": mixedPlaylist("small", 100),
		},
	}
	plan := Plan{
		Version: PlanVersion,
		Categories: []CategoryDef{
			{ID: "talk", Label: "Talk", Target: 0.5},
			{ID: "music", Label: "Music", Target: 0.5},
		},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"pod1", "pod2"}},
			{ID: "music", SourceIDs: []string{"mus1", "mus2"}},
		},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "talk", Weight: 1}, {Pool: "music", Weight: 1}},
		}},
	}
	s := newStation(t, plan, sources, cat, now)
	intent := ProgrammingIntent{
		Pools:   plan.Blocks[0].Pools,
		Targets: map[CategoryID]float64{"talk": 0.5, "music": 0.5},
	}
	shares := s.engine.sourceShares(intent, s.candidates())

	// Two shows, half the day between them, regardless of archive depth.
	for _, id := range []string{"pod1", "pod2"} {
		if got := shares[id]; got < 0.249 || got > 0.251 {
			t.Fatalf("%s share = %.4f, want 0.25 — a strand is not weighted by its depth", id, got)
		}
	}
	// Two shelves, half the day between them, split by what is on them. 300
	// tracks against 100 of the same lengths is three to one.
	if got, want := shares["mus1"], 0.375; got < want-0.01 || got > want+0.01 {
		t.Fatalf("the 300-track playlist got %.4f of the day, want about %.4f", got, want)
	}
	if got, want := shares["mus2"], 0.125; got < want-0.01 || got > want+0.01 {
		t.Fatalf("the 100-track playlist got %.4f of the day, want about %.4f", got, want)
	}
	// And the two halves are still halves.
	if sum := shares["mus1"] + shares["mus2"]; sum < 0.499 || sum > 0.501 {
		t.Fatalf("music's collective share moved to %.4f — depth may only redistribute within a category", sum)
	}

	// An explicit weight still means what it says: twice the share of a shelf
	// the same size, on top of whatever depth already earned it.
	sources[3].Weight = 3
	weighted := newStation(t, plan, sources, cat, now)
	shares = weighted.engine.sourceShares(intent, weighted.candidates())
	if got, want := shares["mus2"], 0.25; got < want-0.01 || got > want+0.01 {
		t.Fatalf("weight 3 on the 100-track playlist gave it %.4f, want about %.4f", got, want)
	}
}
