package catalog

import (
	"slices"
	"testing"
)

// P5 (2026-09-17): a Recently Added album, long-pressed → Add to playlist, put
// TWO tracks in the playlist while its detail card showed ONE. The album was a
// kept copy sharing its id with the drop-folder twin it was kept from: the
// detail card renders from the global track list (explo-filtered), while
// add-to-playlist enumerated the album by id (unfiltered). These tests pin the
// by-id view to the same definition as the lists.

func p5Seed() Seed {
	seed := Seed{
		MusicArtists: []MusicArtist{
			{ID: "artist-radiohead", Name: "Radiohead"},
			{ID: "artist-explo", Name: "Explo Only"},
		},
		MusicAlbums: []MusicAlbum{
			// Mixed: the kept copy AND its drop-folder twin under one album id.
			{ID: "album-mixed", Title: "Pablo Honey", TrackCount: 2, DurationSeconds: 239 + 238, AlbumArtistIDs: []string{"artist-radiohead"}},
			// Fully explo, with two drops.
			{ID: "album-explo", Title: "Weekly Drop", TrackCount: 2, DurationSeconds: 200 + 201, AlbumArtistIDs: []string{"artist-explo"}, IsExplo: true, HiddenFromRecentlyAdded: true},
			// Ordinary library album.
			{ID: "album-real", Title: "The Bends", TrackCount: 1, DurationSeconds: 300, AlbumArtistIDs: []string{"artist-radiohead"}},
		},
		MusicTracks: []MusicTrack{
			{ID: "track-kept", Title: "Creep", AlbumID: "album-mixed", ArtistIDs: []string{"artist-radiohead"}, DurationSeconds: 239},
			{ID: "track-twin", Title: "Creep", AlbumID: "album-mixed", ArtistIDs: []string{"artist-radiohead"}, DurationSeconds: 238, IsExplo: true},
			{ID: "track-drop-1", Title: "Drop One", AlbumID: "album-explo", ArtistIDs: []string{"artist-explo"}, DurationSeconds: 200, IsExplo: true},
			{ID: "track-drop-2", Title: "Drop Two", AlbumID: "album-explo", ArtistIDs: []string{"artist-explo"}, DurationSeconds: 201, IsExplo: true},
			{ID: "track-real", Title: "Just", AlbumID: "album-real", ArtistIDs: []string{"artist-radiohead"}, DurationSeconds: 300},
		},
	}
	DeriveExploArtists(&seed)
	return seed
}

func trackIDs(tracks []MusicTrack) []string {
	ids := make([]string, 0, len(tracks))
	for _, track := range tracks {
		ids = append(ids, track.ID)
	}
	return ids
}

func TestP5AlbumTracksAsSeen(t *testing.T) {
	kept := MusicTrack{ID: "kept"}
	twin := MusicTrack{ID: "twin", IsExplo: true}
	drop := MusicTrack{ID: "drop", IsExplo: true}

	cases := []struct {
		name string
		in   []MusicTrack
		want []string
	}{
		{"library album", []MusicTrack{kept}, []string{"kept"}},
		{"mixed album drops the twin", []MusicTrack{kept, twin}, []string{"kept"}},
		{"mixed album, twin first", []MusicTrack{twin, kept}, []string{"kept"}},
		{"explo album keeps everything", []MusicTrack{drop, twin}, []string{"drop", "twin"}},
		{"empty", nil, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := trackIDs(AlbumTracksAsSeen(tc.in))
			if !slices.Equal(got, tc.want) {
				t.Fatalf("AlbumTracksAsSeen = %v, want %v", got, tc.want)
			}
		})
	}

	// Never mutates its input: the caller's slice is the catalog's own.
	in := []MusicTrack{kept, twin}
	_ = AlbumTracksAsSeen(in)
	if in[1].ID != "twin" {
		t.Fatal("AlbumTracksAsSeen mutated its input")
	}
}

// The by-id album view must list exactly what the global track list would
// attribute to that album — that agreement is the whole bug.
func TestP5MixedAlbumByIDAgreesWithGlobalList(t *testing.T) {
	service := NewService(p5Seed())

	byID := trackIDs(service.MusicTracksForAlbum("album-mixed"))
	if !slices.Equal(byID, []string{"track-kept"}) {
		t.Fatalf("mixed album by id = %v, want [track-kept] (the drop-folder twin leaked)", byID)
	}

	var fromList []string
	for _, track := range service.ListMusicTracksSorted(MusicListOptions{}).Items {
		if track.AlbumID == "album-mixed" {
			fromList = append(fromList, track.ID)
		}
	}
	if !slices.Equal(fromList, byID) {
		t.Fatalf("global list attributes %v to the album, by-id view lists %v", fromList, byID)
	}
}

func TestP5ExploAndLibraryAlbumsUnchangedByID(t *testing.T) {
	service := NewService(p5Seed())

	// An explo album is only ever reached on purpose; it shows every drop.
	if got := trackIDs(service.MusicTracksForAlbum("album-explo")); !slices.Equal(got, []string{"track-drop-1", "track-drop-2"}) {
		t.Fatalf("explo album by id = %v, want both drops", got)
	}
	if got := trackIDs(service.MusicTracksForAlbum("album-real")); !slices.Equal(got, []string{"track-real"}) {
		t.Fatalf("library album by id = %v, want [track-real]", got)
	}
	// The playlist path stays inclusive: the Explore playlist points at drops.
	seed := p5Seed()
	seed.MusicPlaylists = []MusicPlaylist{{ID: "explore", Name: "Explore", System: true, TrackIDs: []string{"track-twin", "track-drop-1"}}}
	if got := trackIDs(NewService(seed).MusicTracksForPlaylist("explore")); !slices.Equal(got, []string{"track-twin", "track-drop-1"}) {
		t.Fatalf("playlist tracks = %v, want the drops it references", got)
	}
}

// The twin never reaches a real artist's page either — the same rule the
// album view now follows, stated once more so the two cannot drift apart.
func TestP5TwinNeverOnRealArtistPage(t *testing.T) {
	service := NewService(p5Seed())
	for _, track := range service.MusicTracksForArtist("artist-radiohead") {
		if track.ID == "track-twin" {
			t.Fatal("drop-folder twin leaked onto the artist's page")
		}
	}
}

func TestP5RecountMixedAlbums(t *testing.T) {
	seed := p5Seed()
	RecountMixedAlbums(&seed)

	byID := map[string]MusicAlbum{}
	for _, album := range seed.MusicAlbums {
		byID[album.ID] = album
	}
	if got := byID["album-mixed"]; got.TrackCount != 1 || got.DurationSeconds != 239 {
		t.Fatalf("mixed album count/duration = %d/%d, want 1/239 (library tracks only)", got.TrackCount, got.DurationSeconds)
	}
	// Albums without a stray keep the scanner's stored values verbatim.
	if got := byID["album-explo"]; got.TrackCount != 2 || got.DurationSeconds != 401 {
		t.Fatalf("explo album count/duration = %d/%d, want the stored 2/401", got.TrackCount, got.DurationSeconds)
	}
	if got := byID["album-real"]; got.TrackCount != 1 || got.DurationSeconds != 300 {
		t.Fatalf("library album count/duration = %d/%d, want the stored 1/300", got.TrackCount, got.DurationSeconds)
	}

	// Safe on the degenerate inputs the loader can hand it.
	RecountMixedAlbums(nil)
	RecountMixedAlbums(&Seed{})
}
