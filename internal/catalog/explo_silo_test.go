package catalog

import "testing"

// siloTestSeed builds a projection with one real artist/album/track and one
// explo artist/album/track, plus a collision artist credited on both.
func siloTestSeed() Seed {
	seed := Seed{
		MusicArtists: []MusicArtist{
			{ID: "artist-real", Name: "Owned Artist"},
			{ID: "artist-explo", Name: "Explo Artist"},
			{ID: "artist-both", Name: "Collision Artist"},
		},
		MusicAlbums: []MusicAlbum{
			{ID: "album-real", Title: "Owned Album", TrackCount: 2, AlbumArtistIDs: []string{"artist-real"}},
			{ID: "album-explo", Title: "Explo Album", TrackCount: 1, AlbumArtistIDs: []string{"artist-explo"}, IsExplo: true, HiddenFromRecentlyAdded: true},
		},
		MusicTracks: []MusicTrack{
			{ID: "track-real", Title: "Owned Song", AlbumID: "album-real", ArtistIDs: []string{"artist-real"}},
			{ID: "track-both", Title: "Owned Feature", AlbumID: "album-real", ArtistIDs: []string{"artist-both"}},
			{ID: "track-explo", Title: "Explo Song", AlbumID: "album-explo", ArtistIDs: []string{"artist-explo", "artist-both"}, IsExplo: true},
		},
	}
	deriveExploArtists(&seed)
	return seed
}

func TestDeriveExploArtists(t *testing.T) {
	seed := siloTestSeed()
	got := map[string]bool{}
	for _, artist := range seed.MusicArtists {
		got[artist.ID] = artist.IsExplo
	}
	if got["artist-real"] {
		t.Fatal("artist-real derived explo; has only real tracks")
	}
	if !got["artist-explo"] {
		t.Fatal("artist-explo not derived explo; every attributable track is explo")
	}
	if got["artist-both"] {
		t.Fatal("artist-both derived explo; one real track credit must keep them real")
	}
}

func TestExploExcludedFromSortedListsAndBrowse(t *testing.T) {
	service := NewService(siloTestSeed())

	artists := service.ListMusicArtistsSorted(MusicListOptions{})
	for _, artist := range artists.Items {
		if artist.ID == "artist-explo" {
			t.Fatal("explo-only artist leaked into the artist list")
		}
	}
	if len(artists.Items) != 2 {
		t.Fatalf("artist list = %d items, want 2", len(artists.Items))
	}

	albums := service.ListMusicAlbumsSorted(MusicListOptions{Sort: MusicListSortRecent})
	for _, album := range albums.Items {
		if album.ID == "album-explo" {
			t.Fatal("explo album leaked into the album list (sort=recent — the Recently Added clone)")
		}
	}

	tracks := service.ListMusicTracksSorted(MusicListOptions{})
	for _, track := range tracks.Items {
		if track.ID == "track-explo" {
			t.Fatal("explo track leaked into the track list")
		}
	}

	// Discovery and Unplayed select never-played newest-first — exactly the
	// explo population; the silo must hold there hardest.
	for _, view := range []MusicBrowseView{MusicBrowseUnplayed, MusicBrowseDiscovery, MusicBrowseRecentlyAdded} {
		results := service.MusicBrowseForUser(nil, nil, nil, nil, view, PageRequest{Limit: 100}, "user-1")
		for _, track := range results.Tracks {
			if track.ID == "track-explo" {
				t.Fatalf("explo track leaked into browse view %q", view)
			}
		}
		for _, album := range results.Albums {
			if album.ID == "album-explo" {
				t.Fatalf("explo album leaked into browse view %q", view)
			}
		}
		for _, artist := range results.Artists {
			if artist.ID == "artist-explo" {
				t.Fatalf("explo artist leaked into browse view %q", view)
			}
		}
	}
}

func TestExploByIDAndRelationRules(t *testing.T) {
	service := NewService(siloTestSeed())

	// By-ID stays resolvable — the Explo tab and Explore playlist depend on it.
	if _, err := service.MusicAlbum("album-explo"); err != nil {
		t.Fatalf("explo album not resolvable by ID: %v", err)
	}
	if _, err := service.MusicTrack("track-explo"); err != nil {
		t.Fatalf("explo track not resolvable by ID: %v", err)
	}
	if got := service.MusicTracksForAlbum("album-explo"); len(got) != 1 {
		t.Fatalf("explo album detail tracks = %d, want 1 (by-ID surfaces stay inclusive)", len(got))
	}

	// A real (collision) artist's page never mixes in their explo credits...
	for _, track := range service.MusicTracksForArtist("artist-both") {
		if track.ID == "track-explo" {
			t.Fatal("explo track leaked onto a real artist's page")
		}
	}
	for _, album := range service.MusicArtistAppearsOnAlbums("artist-both") {
		if album.ID == "album-explo" {
			t.Fatal("explo album leaked onto a real artist's Appears On rail")
		}
	}
	// ...but an explo-only artist's page shows their explo albums, else it
	// would render empty when reached from the Explo tab.
	if albums := service.MusicAlbumsForArtist("artist-explo"); len(albums) != 1 || albums[0].ID != "album-explo" {
		t.Fatalf("explo-only artist albums = %v, want [album-explo]", albums)
	}
}

func TestOverviewExcludesExplo(t *testing.T) {
	service := NewService(siloTestSeed())
	overview := service.Overview()
	if overview.Music.ArtistCount != 2 || overview.Music.AlbumCount != 1 || overview.Music.TrackCount != 2 {
		t.Fatalf("overview counts artists=%d albums=%d tracks=%d, want 2/1/2",
			overview.Music.ArtistCount, overview.Music.AlbumCount, overview.Music.TrackCount)
	}
}

func TestWithoutExploHelpersPreserveNonExploSlices(t *testing.T) {
	tracks := []MusicTrack{{ID: "a"}, {ID: "b"}}
	if got := WithoutExploTracks(tracks); len(got) != 2 {
		t.Fatalf("filter dropped non-explo tracks: %d", len(got))
	}
	mixed := []MusicTrack{{ID: "a"}, {ID: "x", IsExplo: true}, {ID: "b"}}
	got := WithoutExploTracks(mixed)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("filter wrong: %v", got)
	}
	// The input slice is never mutated.
	if mixed[1].ID != "x" {
		t.Fatal("filter mutated its input")
	}
}
