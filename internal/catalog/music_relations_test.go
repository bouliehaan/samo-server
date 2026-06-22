package catalog

import "testing"

func TestMusicAlbumsForArtistMatchesAlbumArtistOnly(t *testing.T) {
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{
			{ID: "album-1", Title: "One", ArtistIDs: []string{"artist-1"}, TrackCount: 1},
			{ID: "album-2", Title: "Two", AlbumArtistIDs: []string{"artist-1"}, TrackCount: 3},
			{ID: "album-3", Title: "Three", AlbumArtistIDs: []string{"artist-2"}, TrackCount: 1},
		},
	})

	albums := service.MusicAlbumsForArtist("artist-1")
	if len(albums) != 1 {
		t.Fatalf("album count = %d, want 1 (album artist only)", len(albums))
	}
	if albums[0].ID != "album-2" {
		t.Fatalf("album = %q, want album-2", albums[0].ID)
	}
}

func TestMusicArtistAppearsOnAlbums(t *testing.T) {
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{
			// Artist's own album — must NOT appear in "Appears On".
			{ID: "own", Title: "Own", AlbumArtistIDs: []string{"artist-1"}, TrackCount: 2, ReleaseYear: 2018},
			// Compilation: artist credited on a track, not the album artist.
			{ID: "comp", Title: "Comp", AlbumArtistIDs: []string{"various"}, TrackCount: 12, ReleaseYear: 2022},
			// Feature on someone else's album.
			{ID: "feat", Title: "Feat", AlbumArtistIDs: []string{"artist-2"}, TrackCount: 10, ReleaseYear: 2020},
			// Album the artist has nothing to do with.
			{ID: "other", Title: "Other", AlbumArtistIDs: []string{"artist-3"}, TrackCount: 9, ReleaseYear: 2021},
		},
		MusicTracks: []MusicTrack{
			{ID: "t-own", AlbumID: "own", ArtistIDs: []string{"artist-1"}},
			{ID: "t-comp", AlbumID: "comp", ArtistIDs: []string{"artist-1"}},
			{ID: "t-feat", AlbumID: "feat", ArtistIDs: []string{"artist-2", "artist-1"}},
			{ID: "t-other", AlbumID: "other", ArtistIDs: []string{"artist-3"}},
		},
	})

	albums := service.MusicArtistAppearsOnAlbums("artist-1")
	if len(albums) != 2 {
		t.Fatalf("appears-on count = %d, want 2 (comp + feat); got %#v", len(albums), albums)
	}
	// Ordered newest-first: comp (2022) before feat (2020).
	if albums[0].ID != "comp" || albums[1].ID != "feat" {
		t.Fatalf("appears-on order = [%q, %q], want [comp, feat]", albums[0].ID, albums[1].ID)
	}
}

func TestSetMusicArtistMetaAndNameLookup(t *testing.T) {
	service := NewService(Seed{
		MusicArtists: []MusicArtist{
			{ID: "artist-1", Name: "Phoebe Bridgers"},
			{ID: "artist-2", Name: "boygenius"},
		},
	})

	if id, ok := service.MusicArtistIDByName("  PHOEBE   bridgers "); !ok || id != "artist-1" {
		t.Fatalf("name lookup = %q %v, want artist-1 true", id, ok)
	}
	if _, ok := service.MusicArtistIDByName("Nobody"); ok {
		t.Fatalf("unknown name resolved unexpectedly")
	}

	service.SetMusicArtistMeta("artist-1", "A short bio.", []SimilarArtistRef{{ID: "artist-2", Name: "boygenius"}})
	artist, err := service.MusicArtist("artist-1")
	if err != nil {
		t.Fatalf("MusicArtist: %v", err)
	}
	if artist.Biography != "A short bio." {
		t.Fatalf("biography = %q", artist.Biography)
	}
	if len(artist.SimilarArtists) != 1 || artist.SimilarArtists[0].ID != "artist-2" {
		t.Fatalf("similar = %#v", artist.SimilarArtists)
	}

	// Empty inputs must not erase existing data.
	service.SetMusicArtistMeta("artist-1", "", nil)
	artist, _ = service.MusicArtist("artist-1")
	if artist.Biography != "A short bio." || len(artist.SimilarArtists) != 1 {
		t.Fatalf("empty patch erased data: %#v", artist)
	}
}

func TestMusicTracksForAlbumAndPlaylist(t *testing.T) {
	service := NewService(Seed{
		MusicTracks: []MusicTrack{
			{ID: "track-1", AlbumID: "album-1", Title: "A"},
			{ID: "track-2", AlbumID: "album-1", Title: "B"},
			{ID: "track-3", AlbumID: "album-2", Title: "C"},
		},
		MusicPlaylists: []MusicPlaylist{{
			ID:       "playlist-1",
			Name:     "Mix",
			TrackIDs: []string{"track-3", "track-1"},
		}},
	})

	tracks := service.MusicTracksForAlbum("album-1")
	if len(tracks) != 2 {
		t.Fatalf("album track count = %d, want 2", len(tracks))
	}

	playlistTracks := service.MusicTracksForPlaylist("playlist-1")
	if len(playlistTracks) != 2 || playlistTracks[0].ID != "track-3" {
		t.Fatalf("playlist tracks = %#v, want track-3 then track-1", playlistTracks)
	}
}

func TestResolveMusicCoverArtIDPrefersTrackThenAlbum(t *testing.T) {
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{{
			ID:     "album-1",
			Images: []Image{{Path: "/covers/album.jpg"}},
		}},
		MusicTracks: []MusicTrack{{
			ID:      "track-1",
			AlbumID: "album-1",
			Images:  []Image{{Path: "/covers/track.jpg"}},
		}},
	})

	id, images := service.ResolveMusicCoverArtID("track-1")
	if id != "track-1" || len(images) != 1 || images[0].Path != "/covers/track.jpg" {
		t.Fatalf("track cover = %q %#v", id, images)
	}

	id, images = service.ResolveMusicCoverArtID("album-1")
	if id != "album-1" || images[0].Path != "/covers/album.jpg" {
		t.Fatalf("album cover = %q %#v", id, images)
	}
}

func TestResolveMusicCoverArtIDFallsBackToTrackBackedAlbumArt(t *testing.T) {
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{{ID: "album-1", Title: "Album"}},
		MusicTracks: []MusicTrack{{
			ID:      "track-1",
			AlbumID: "album-1",
			Images:  []Image{{Path: "/covers/track.jpg"}},
		}},
	})

	id, images := service.ResolveMusicCoverArtID("album-1")
	if id != "album-1" || len(images) != 1 || images[0].Path != "/covers/track.jpg" {
		t.Fatalf("album cover fallback = %q %#v", id, images)
	}
}
