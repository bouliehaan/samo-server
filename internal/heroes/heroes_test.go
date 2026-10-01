package heroes

import (
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

var now = time.Date(2026, time.September, 19, 16, 0, 0, 0, time.UTC)

func at(d time.Duration) *time.Time {
	t := now.Add(d)
	return &t
}

func track(id, artist, cover string, added time.Duration) catalog.MusicTrack {
	return catalog.MusicTrack{
		ID:              id,
		Title:           "Song " + id,
		DisplayArtist:   artist,
		AlbumID:         "album-" + cover,
		DurationSeconds: 200,
		Images:          []catalog.Image{{ID: cover}},
		AddedAt:         at(added),
	}
}

const day = 24 * time.Hour

func dropInput(tracks []catalog.MusicTrack, state catalog.PlaybackState) Input {
	return Input{
		Playlists: []catalog.MusicPlaylist{
			{ID: "pl-mine", Name: "Good songs", TrackCount: 2},
			{ID: "pl-explore", Name: "Explore", System: true, TrackCount: len(tracks)},
		},
		PlaylistTracks: func(id string) []catalog.MusicTrack {
			if id == "pl-explore" {
				return tracks
			}
			return nil
		},
		PlaylistStates: map[string]catalog.PlaybackState{"pl-explore": state},
	}
}

func TestExploreCountsThisWeeksArrivalsAndFansDistinctSleeves(t *testing.T) {
	// A fifty-track batch on Tuesday and one straggler this morning: the
	// eyebrow counts, it does not say "dropped today". Three songs off one
	// album fan out as ONE sleeve.
	tracks := []catalog.MusicTrack{
		track("t1", "Lord Huron", "cover_a", -4*day),
		track("t2", "Lord Huron", "cover_a", -4*day),
		track("t3", "MGMT", "cover_b", -4*day),
		track("t4", "Joji", "cover_c", -20*day),
		track("t5", "Modest Mouse", "cover_d", -time.Hour),
		track("t6", "Grouplove", "cover_e", -4*day),
	}
	heroes := Rank(dropInput(tracks, catalog.PlaybackState{}), now)
	if len(heroes) != 1 || heroes[0].Kind != KindExplore {
		t.Fatalf("heroes = %+v, want the drop alone", heroes)
	}
	hero := heroes[0]
	if hero.Eyebrow != "Fresh drop · 5 new this week" {
		t.Fatalf("eyebrow = %q", hero.Eyebrow)
	}
	if hero.Subtitle != "New music found for you — Lord Huron, MGMT, Joji and more" {
		t.Fatalf("subtitle = %q", hero.Subtitle)
	}
	if hero.Meta != "6 tracks · 20m" {
		t.Fatalf("meta = %q", hero.Meta)
	}
	if got := sleeveIDs(hero); len(got) != 4 || got[0] != "cover_a" || got[1] != "cover_b" || got[2] != "cover_c" || got[3] != "cover_d" {
		t.Fatalf("sleeves = %v", got)
	}
	if hero.Action != ActionShuffle || hero.Target != (Target{Type: "playlist", ID: "pl-explore"}) {
		t.Fatalf("target/action = %+v %q", hero.Target, hero.Action)
	}
	if hero.Score != 0.9 {
		t.Fatalf("an unheard drop should own the top, score = %v", hero.Score)
	}
}

func TestExploreStaysButStepsBackOncePlayedThrough(t *testing.T) {
	tracks := []catalog.MusicTrack{track("t1", "Lord Huron", "cover_a", -20*day)}
	played := catalog.PlaybackState{LastPlayedAt: at(-2 * day)}
	heroes := Rank(dropInput(tracks, played), now)
	if len(heroes) != 1 {
		t.Fatalf("the drop is always a candidate; heroes = %+v", heroes)
	}
	if heroes[0].Score != 0.5 {
		t.Fatalf("a drop played since its last arrival is not news, score = %v", heroes[0].Score)
	}
	if heroes[0].Eyebrow != "Fresh drop · updated Aug 30" {
		t.Fatalf("eyebrow = %q", heroes[0].Eyebrow)
	}
}

func TestNoDropNoCard(t *testing.T) {
	in := dropInput(nil, catalog.PlaybackState{})
	if heroes := Rank(in, now); len(heroes) != 0 {
		t.Fatalf("an empty drop earns nothing, got %+v", heroes)
	}
}

func episodeInput(published time.Duration, state catalog.PlaybackState) Input {
	episodes := []catalog.PodcastEpisode{
		{ID: "ep-new", PodcastID: "show-a", PodcastTitle: "The Dude Grows Show", Title: "Mother Plants", DurationSeconds: 4320, PublishedAt: at(published)},
	}
	states := map[string]catalog.PlaybackState{"ep-new": state}
	// Three finished episodes make the show one the listener actually
	// finishes; one abandoned keeps the rate under 1.
	for i, id := range []string{"ep-1", "ep-2", "ep-3", "ep-4"} {
		episodes = append(episodes, catalog.PodcastEpisode{ID: id, PodcastID: "show-a", Title: id, PublishedAt: at(-time.Duration(30+i) * day)})
		states[id] = catalog.PlaybackState{Completed: i < 3, PlayCount: 1}
	}
	// A show that was started once and never finished is not S-tier, however
	// new its episode.
	episodes = append(episodes,
		catalog.PodcastEpisode{ID: "ep-meh-new", PodcastID: "show-b", PodcastTitle: "Meh", Title: "Meh 9", PublishedAt: at(-time.Hour)},
		catalog.PodcastEpisode{ID: "ep-meh-1", PodcastID: "show-b", Title: "Meh 1", PublishedAt: at(-40 * day)},
	)
	states["ep-meh-1"] = catalog.PlaybackState{ProgressSeconds: 300}
	return Input{
		Podcasts:      []catalog.PodcastItem{{ID: "show-a", Podcast: &catalog.PodcastMetadata{Title: "The Dude Grows Show"}}, {ID: "show-b"}},
		Episodes:      episodes,
		EpisodeStates: states,
	}
}

func TestNewEpisodeOfAFinishedShowEarnsACard(t *testing.T) {
	heroes := Rank(episodeInput(-3*time.Hour, catalog.PlaybackState{}), now)
	if len(heroes) != 1 || heroes[0].Kind != KindEpisode {
		t.Fatalf("heroes = %+v, want the one S-tier episode", heroes)
	}
	hero := heroes[0]
	if hero.Eyebrow != "New episode · 3h ago" || hero.Title != "Mother Plants" || hero.Subtitle != "The Dude Grows Show" || hero.Meta != "1h 12m" {
		t.Fatalf("copy = %q / %q / %q / %q", hero.Eyebrow, hero.Title, hero.Subtitle, hero.Meta)
	}
	if hero.Target != (Target{Type: "episode", ID: "ep-new"}) || hero.Action != ActionPlay {
		t.Fatalf("target/action = %+v %q", hero.Target, hero.Action)
	}
	if len(hero.Sleeves) != 1 || hero.Sleeves[0].URL != "/api/v1/podcasts/shows/show-a/cover" {
		t.Fatalf("sleeves = %+v", hero.Sleeves)
	}
}

func TestEpisodeCardGoesOnceStartedOrStale(t *testing.T) {
	if heroes := Rank(episodeInput(-3*time.Hour, catalog.PlaybackState{ProgressSeconds: 60}), now); len(heroes) != 0 {
		t.Fatalf("a started episode is pick-up territory, not news: %+v", heroes)
	}
	if heroes := Rank(episodeInput(-9*day, catalog.PlaybackState{}), now); len(heroes) != 0 {
		t.Fatalf("a week-old episode nobody played has had its chance: %+v", heroes)
	}
}

func TestFreshDropOutranksEpisodeOutranksPlayedDrop(t *testing.T) {
	in := episodeInput(-3*time.Hour, catalog.PlaybackState{})
	drop := dropInput([]catalog.MusicTrack{track("t1", "MGMT", "cover_a", -2*day)}, catalog.PlaybackState{})
	in.Playlists, in.PlaylistTracks, in.PlaylistStates = drop.Playlists, drop.PlaylistTracks, drop.PlaylistStates

	heroes := Rank(in, now)
	if len(heroes) != 2 || heroes[0].Kind != KindExplore || heroes[1].Kind != KindEpisode {
		t.Fatalf("unheard drop first, then the episode: %+v", kinds(heroes))
	}

	in.PlaylistStates["pl-explore"] = catalog.PlaybackState{LastPlayedAt: at(-time.Hour)}
	heroes = Rank(in, now)
	if len(heroes) != 2 || heroes[0].Kind != KindEpisode || heroes[1].Kind != KindExplore {
		t.Fatalf("once the drop is heard, the new episode leads: %+v", kinds(heroes))
	}
}

func TestSeasonBringsTheListenersOwnPlaylistForward(t *testing.T) {
	tracks := []catalog.MusicTrack{
		track("x1", "Bing Crosby", "cover_x", -300*day),
		track("x2", "The Beach Boys", "cover_y", -300*day),
	}
	in := Input{
		Playlists: []catalog.MusicPlaylist{
			{ID: "pl-xmas", Name: "Cool Christmas", TrackCount: 2, UpdatedAt: at(-100 * day)},
			{ID: "pl-old-xmas", Name: "Christmas 2019", TrackCount: 2, UpdatedAt: at(-900 * day)},
			{ID: "pl-empty", Name: "Christmas (wip)", TrackCount: 0, UpdatedAt: at(-day)},
		},
		PlaylistTracks: func(string) []catalog.MusicTrack { return tracks },
	}

	december := time.Date(2026, time.December, 3, 9, 0, 0, 0, time.UTC)
	heroes := Rank(in, december)
	if len(heroes) != 1 || heroes[0].Kind != KindSeason {
		t.Fatalf("heroes = %+v", heroes)
	}
	if heroes[0].Target.ID != "pl-xmas" || heroes[0].Eyebrow != "Almost Christmas" || heroes[0].Title != "Cool Christmas" {
		t.Fatalf("card = %+v", heroes[0])
	}
	if heroes[0].Subtitle != "Your Christmas playlist — Bing Crosby, The Beach Boys" {
		t.Fatalf("subtitle = %q", heroes[0].Subtitle)
	}

	eve := time.Date(2026, time.December, 24, 20, 0, 0, 0, time.UTC)
	if got := Rank(in, eve)[0].Eyebrow; got != "Merry Christmas" {
		t.Fatalf("on the day the eyebrow stops saying almost: %q", got)
	}

	july := time.Date(2026, time.July, 3, 9, 0, 0, 0, time.UTC)
	if heroes := Rank(in, july); len(heroes) != 0 {
		t.Fatalf("no season in July: %+v", heroes)
	}
}

func sleeveIDs(hero Hero) []string {
	ids := make([]string, 0, len(hero.Sleeves))
	for _, sleeve := range hero.Sleeves {
		ids = append(ids, sleeve.ID)
	}
	return ids
}

func kinds(heroes []Hero) []Kind {
	out := make([]Kind, 0, len(heroes))
	for _, hero := range heroes {
		out = append(out, hero.Kind)
	}
	return out
}
