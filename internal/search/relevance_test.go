package search

import (
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The report this ranker exists for: the library has "Kiss Me" (Sixpence None
// The Richer), five copies of "Kiss Me Quick" on Elvis compilations and "Kiss
// me where it smells funny". Searching "Kiss Me" returned the copies, then the
// Bloodhound Gang, and never the song — the old scorer only looked at where
// each query word first appeared, so every title starting "Kiss Me" tied and
// the client's six-row limit fell on catalog order.
func TestSearchMusicExactTitleOutranksLongerTitles(t *testing.T) {
	tracks := []catalog.MusicTrack{
		{ID: "quick-1", Title: "Kiss Me Quick", AlbumTitle: "Pot Luck", DisplayArtist: "Elvis Presley"},
		{ID: "quick-2", Title: "Kiss Me Quick", AlbumTitle: "Elvis' Gold Records Volume 4", DisplayArtist: "Elvis Presley"},
		{ID: "quick-3", Title: "Kiss Me Quick", AlbumTitle: "The Essential Elvis Presley", DisplayArtist: "Elvis Presley"},
		{ID: "quick-4", Title: "Kiss Me Quick", AlbumTitle: "Elvis 60s Hits", DisplayArtist: "Elvis Presley"},
		{ID: "quick-5", Title: "Kiss Me Quick", AlbumTitle: "Elvis: The Collection", DisplayArtist: "Elvis Presley"},
		{ID: "funny", Title: "Kiss me where it smells funny", AlbumTitle: "Use Your Fingers", DisplayArtist: "Bloodhound Gang"},
		{ID: "cure", Title: "Just Like Heaven", AlbumTitle: "Kiss Me, Kiss Me, Kiss Me", DisplayArtist: "The Cure"},
		{ID: "sixpence", Title: "Kiss Me", AlbumTitle: "This Beautiful Mess", DisplayArtist: "Sixpence None The Richer"},
	}
	service := New()
	service.Rebuild(catalog.Seed{MusicTracks: tracks})

	results := service.SearchMusicText("Kiss Me", catalog.PageRequest{Limit: 6})
	got := trackIDs(results.Tracks)
	want := []string{"sixpence", "quick-1", "quick-2", "quick-3", "quick-4", "quick-5"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tracks = %v, want %v", got, want)
	}

	all := service.SearchMusicText("Kiss Me", catalog.PageRequest{Limit: 10}).Tracks
	if got := trackIDs(all)[6:]; strings.Join(got, ",") != "funny,cure" {
		t.Fatalf("tail = %v, want the long title then the album-only match", got)
	}
}

func TestSearchMusicPlayCountBreaksTitleTies(t *testing.T) {
	tracks := []catalog.MusicTrack{
		{ID: "unplayed-a", Title: "Kiss Me Quick", DisplayArtist: "Elvis Presley"},
		{ID: "played", Title: "Kiss Me Quick", DisplayArtist: "Elvis Presley"},
		{ID: "unplayed-b", Title: "Kiss Me Quick", DisplayArtist: "Elvis Presley"},
	}
	service := New()
	service.Rebuild(catalog.Seed{MusicTracks: tracks})

	overlay := PlaybackOverlay{Tracks: map[string]catalog.PlaybackState{
		"played": {PlayCount: 12},
	}}
	results := service.SearchMusic(MusicQuery{Text: "kiss me quick", Page: catalog.PageRequest{Limit: 10}, Sort: SortRelevance}, overlay)
	if got := trackIDs(results.Tracks); got[0] != "played" {
		t.Fatalf("tracks = %v, want the played copy first", got)
	}
}

func TestSearchMusicTitleThenArtistQuery(t *testing.T) {
	tracks := []catalog.MusicTrack{
		{ID: "quick", Title: "Kiss Me Quick", DisplayArtist: "Elvis Presley", Genres: []string{"Sixpence Tribute"}},
		{ID: "sixpence", Title: "Kiss Me", DisplayArtist: "Sixpence None The Richer"},
		{ID: "adele", Title: "Hello", DisplayArtist: "Adele"},
		{ID: "beatles", Title: "Hello, Goodbye", DisplayArtist: "The Beatles", Tags: []string{"adele-cover"}},
	}
	service := New()
	service.Rebuild(catalog.Seed{MusicTracks: tracks})

	if got := trackIDs(service.SearchMusicText("kiss me sixpence", catalog.PageRequest{Limit: 10}).Tracks); got[0] != "sixpence" {
		t.Fatalf("kiss me sixpence = %v, want the whole title plus artist first", got)
	}
	if got := trackIDs(service.SearchMusicText("hello adele", catalog.PageRequest{Limit: 10}).Tracks); got[0] != "adele" {
		t.Fatalf("hello adele = %v, want the whole title plus artist first", got)
	}
}

func TestRelevanceTiers(t *testing.T) {
	rel := newRelevance("kiss me")
	ladder := []struct {
		title string
		tier  int
	}{
		{"Kiss Me", tierExact},
		{"Kiss Me Quick", tierPrefix},
		{"Please Kiss Me", tierPhrase},
		{"Me Kiss", tierWords},
		{"Kissing Men", tierSubstrings},
		{"Kiss", tierPartial / 2},
		{"Nothing Here", 0},
	}
	previous := -1
	for i, step := range ladder {
		score := rel.score(step.title, "")
		if score < step.tier || (step.tier > 0 && score >= step.tier+100) {
			t.Fatalf("%q scored %d, want tier %d", step.title, score, step.tier)
		}
		if i > 0 && score >= previous {
			t.Fatalf("%q (%d) should rank below %q (%d)", step.title, score, ladder[i-1].title, previous)
		}
		previous = score
	}
	if rel.score("KISS ME!", "") != rel.score("Kiss Me", "") {
		t.Fatal("case and punctuation should not affect the score")
	}
}

func TestRelevanceLeadingArticleAndStopwords(t *testing.T) {
	kiss := newRelevance("kiss")
	if plain, article := kiss.score("Kiss", ""), kiss.score("The Kiss", ""); !(plain > article && article > kiss.score("Kiss Me", "")) {
		t.Fatalf("kiss: Kiss=%d The Kiss=%d Kiss Me=%d, want that order", plain, article, kiss.score("Kiss Me", ""))
	}
	theKiss := newRelevance("the kiss")
	if full, bare := theKiss.score("The Kiss", ""), theKiss.score("Kiss", ""); !(full > bare && bare >= tierExact) {
		t.Fatalf("the kiss: The Kiss=%d Kiss=%d, want both exact with the full title ahead", full, bare)
	}
}

func TestRelevanceSecondaryTextIsOnlyTheFloor(t *testing.T) {
	rel := newRelevance("kiss me")
	exact := rel.score("Kiss Me", "This Beautiful Mess Sixpence None The Richer")
	exactSameAlbum := rel.score("Kiss Me", "Kiss Me Sixpence None The Richer")
	prefix := rel.score("Kiss Me Quick", "Pot Luck Elvis Presley")
	articleWithAlbum := rel.score("The Kiss", "Kiss Me Kiss Me Kiss Me The Cure")
	albumOnly := rel.score("Just Like Heaven", "Kiss Me Kiss Me Kiss Me The Cure")
	scattered := rel.score("Lovesong", "Disintegration The Cure Kissing Metal")
	if exact != exactSameAlbum {
		t.Fatalf("album text changed an exact title's score: %d vs %d", exact, exactSameAlbum)
	}
	if !(exact > prefix && prefix > articleWithAlbum) {
		t.Fatalf("exact=%d prefix=%d The Kiss+album=%d, want the title tier to decide", exact, prefix, articleWithAlbum)
	}
	if !(albumOnly == floorPhrase && albumOnly > scattered && scattered > 0) {
		t.Fatalf("album-only=%d scattered=%d, want the phrase floor above the word floor", albumOnly, scattered)
	}
	if albumOnly >= tierPartial/2 && albumOnly >= rel.score("Kissing Men", "") {
		t.Fatalf("album-only=%d should sit below any title that carries every query word", albumOnly)
	}
}

func TestSearchAudiobooksRankTitleFirst(t *testing.T) {
	service := New()
	service.Rebuild(catalog.Seed{Audiobooks: []catalog.AudiobookItem{
		{ID: "dune-messiah", Book: &catalog.BookMetadata{Title: "Dune Messiah"}},
		{ID: "children", Book: &catalog.BookMetadata{Title: "Children of Dune", Description: "Dune continues."}},
		{ID: "dune", Book: &catalog.BookMetadata{Title: "Dune"}},
	}})
	results := service.SearchAudiobooksText("dune", catalog.PageRequest{Limit: 10})
	got := make([]string, 0, len(results.Audiobooks))
	for _, item := range results.Audiobooks {
		got = append(got, item.ID)
	}
	if strings.Join(got, ",") != "dune,dune-messiah,children" {
		t.Fatalf("audiobooks = %v", got)
	}
}

func trackIDs(tracks []catalog.MusicTrack) []string {
	ids := make([]string, 0, len(tracks))
	for _, track := range tracks {
		ids = append(ids, track.ID)
	}
	return ids
}

func TestRelevanceTitleInQueryNeedsTheRestOfTheQuery(t *testing.T) {
	kissMe := newRelevance("kiss me")
	// "me" is buried inside "Home", not a word of it: "Kiss" stays a partial
	// title and ranks below a title that carries the whole phrase.
	accident := kissMe.score("Kiss", "Prince Home Funk")
	if accident >= tierTitleInQuery {
		t.Fatalf("Kiss / Home scored %d, want below %d", accident, tierTitleInQuery)
	}
	if phrase := kissMe.score("Please Kiss Me", ""); accident >= phrase {
		t.Fatalf("Kiss / Home (%d) should rank below Please Kiss Me (%d)", accident, phrase)
	}

	helloAdele := newRelevance("hello adele")
	if got := helloAdele.score("Hello", "Adele 25 Pop"); got < tierTitleInQuery || got > tierTitleInQuery+maxCoverage {
		t.Fatalf("Hello / Adele scored %d, want the title-in-query tier", got)
	}
}
