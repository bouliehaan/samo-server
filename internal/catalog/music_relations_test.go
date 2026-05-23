package catalog

import "testing"

func TestMusicAlbumsForArtistMatchesAlbumAndAlbumArtistIDs(t *testing.T) {
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{
			{ID: "album-1", Title: "One", ArtistIDs: []string{"artist-1"}},
			{ID: "album-2", Title: "Two", AlbumArtistIDs: []string{"artist-1"}},
			{ID: "album-3", Title: "Three", ArtistIDs: []string{"artist-2"}},
		},
	})

	albums := service.MusicAlbumsForArtist("artist-1")
	if len(albums) != 2 {
		t.Fatalf("album count = %d, want 2", len(albums))
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
