package heroes

import (
	"github.com/bouliehaan/samo-server/internal/catalog"
	"reflect"
	"testing"
	"time"
)

func TestFourDayOldEpisodeNeverBecomesANewEpisodeHero(t *testing.T) {
	if got := Rank(episodeInput(-4*day, catalog.PlaybackState{}), now); len(got) != 0 {
		t.Fatalf("stale episode: %+v", got)
	}
}

func TestCompletedRadioAiringSuppressesHeroWithoutChangingPersonalProgress(t *testing.T) {
	in := episodeInput(-time.Hour, catalog.PlaybackState{})
	in.RadioEpisodeStates = map[string]catalog.PlaybackState{"ep-new": {Completed: true}}
	if got := Rank(in, now); len(got) != 0 {
		t.Fatalf("aired episode: %+v", got)
	}
	if in.EpisodeStates["ep-new"].Completed {
		t.Fatal("radio changed personal completion")
	}
}

func TestBookResumeRequiresRecentMeaningfulUnfinishedProgress(t *testing.T) {
	book := catalog.AudiobookItem{ID: "book", Book: &catalog.BookMetadata{Title: "The book"}, DurationSeconds: 7200}
	for _, tc := range []struct {
		name  string
		state catalog.PlaybackState
		want  bool
	}{
		{"reading", catalog.PlaybackState{ProgressSeconds: 1800, LastPlayedAt: at(-day)}, true},
		{"complete", catalog.PlaybackState{Completed: true, ProgressSeconds: 1800, LastPlayedAt: at(-day)}, false},
		{"abandoned", catalog.PlaybackState{ProgressSeconds: 1800, LastPlayedAt: at(-40 * day)}, false},
		{"accidental tap", catalog.PlaybackState{ProgressSeconds: 2, LastPlayedAt: at(-day)}, false},
		{"credits", catalog.PlaybackState{ProgressSeconds: 7100, LastPlayedAt: at(-day)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Rank(Input{Books: []catalog.AudiobookItem{book}, BookStates: map[string]catalog.PlaybackState{"book": tc.state}}, now)
			if (len(got) == 1) != tc.want {
				t.Fatalf("heroes=%+v", got)
			}
			if tc.want && (got[0].Target.Type != "audiobook" || got[0].Meta != "1h 30m left") {
				t.Fatalf("resume=%+v", got[0])
			}
		})
	}
}

func TestPersonalLibraryAndPlaylistsProvideRealAlternatives(t *testing.T) {
	in := Input{
		Albums: []catalog.MusicAlbum{
			{ID: "favorite", Title: "An old favorite", TrackCount: 8},
			{ID: "new", Title: "An unheard record", TrackCount: 10, AddedAt: at(-day)},
			{ID: "just-played", Title: "Played today", TrackCount: 10},
		},
		AlbumStates: map[string]catalog.PlaybackState{
			"favorite":    {Favorite: true, LastPlayedAt: at(-60 * day)},
			"just-played": {Favorite: true, LastPlayedAt: at(-time.Hour)},
		},
		Playlists:      []catalog.MusicPlaylist{{ID: "mix", Name: "Driving", TrackCount: 5}},
		PlaylistStates: map[string]catalog.PlaybackState{"mix": {PlayCount: 2}},
		PlaylistTracks: func(string) []catalog.MusicTrack { return []catalog.MusicTrack{track("t", "Artist", "cover", -day)} },
	}
	got := Rank(in, now)
	kinds := map[Kind]bool{}
	for _, hero := range got {
		kinds[hero.Kind] = true
		if hero.Target.ID == "just-played" {
			t.Fatal("recommending what was just played")
		}
	}
	if len(got) != 3 || !kinds[KindRediscover] || !kinds[KindLibrary] || !kinds[KindPlaylist] {
		t.Fatalf("heroes=%+v", got)
	}
}

func TestVisitIsStableAndRecentPickStepsAside(t *testing.T) {
	in := dropInput([]catalog.MusicTrack{track("t", "Artist", "cover", -day)}, catalog.PlaybackState{})
	in.Albums = []catalog.MusicAlbum{{ID: "album", Title: "A record", TrackCount: 8, AddedAt: at(-day)}}
	in.SessionKey = "user:visit-1"
	first := Rank(in, now)
	if !reflect.DeepEqual(first, Rank(in, now)) {
		t.Fatal("same visit reshuffled")
	}
	in.RecentlyShown = []string{first[0].Target.Type + ":" + first[0].Target.ID}
	in.SessionKey = "user:visit-2"
	second := Rank(in, now)
	if second[0].Target == first[0].Target {
		t.Fatalf("consecutive visits repeated: %+v", second)
	}
}

func TestPlayingOneExploreTrackDoesNotConsumeTheWholeDrop(t *testing.T) {
	in := dropInput([]catalog.MusicTrack{
		track("t1", "One", "c1", -day), track("t2", "Two", "c2", -day),
	}, catalog.PlaybackState{LastPlayedAt: at(-time.Minute)})
	in.TrackStates = map[string]catalog.PlaybackState{"t1": {LastPlayedAt: at(-time.Minute)}}
	if got := Rank(in, now)[0].Score; got != 0.9 {
		t.Fatalf("unheard tracks lost their freshness: %v", got)
	}
	in.TrackStates["t2"] = catalog.PlaybackState{LastPlayedAt: at(-time.Minute)}
	if got := Rank(in, now)[0].Score; got != 0.5 {
		t.Fatalf("heard drop is still fresh: %v", got)
	}
}

func TestOldUnheardExploreDoesNotKeepFreshPriorityForever(t *testing.T) {
	in := dropInput([]catalog.MusicTrack{track("t", "Artist", "cover", -30*day)}, catalog.PlaybackState{})
	if got := Rank(in, now)[0].Score; got != 0.5 {
		t.Fatalf("stale drop=%v", got)
	}
}

func TestEqualScoresHaveStableOrderAndBoundedAlternatives(t *testing.T) {
	in := Input{}
	for _, id := range []string{"z", "b", "a", "y"} {
		in.Albums = append(in.Albums, catalog.MusicAlbum{ID: id, Title: id, TrackCount: 1})
	}
	got := Rank(in, now)
	if len(got) != 2 || got[0].Target.ID != "a" || got[1].Target.ID != "b" {
		t.Fatalf("heroes=%+v", got)
	}
}
