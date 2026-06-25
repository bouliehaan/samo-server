package catalog

import (
	"testing"
	"time"
)

func TestMusicTracksForArtistIncludesAlbumArtistTracks(t *testing.T) {
	service := NewService(Seed{
		MusicArtists: []MusicArtist{{ID: "artist-1", Name: "Main"}},
		MusicAlbums: []MusicAlbum{
			{ID: "album-1", Title: "Via Album", AlbumArtistIDs: []string{"artist-1"}, TrackCount: 1},
		},
		MusicTracks: []MusicTrack{
			{ID: "track-1", Title: "Direct", ArtistIDs: []string{"artist-1"}},
			{ID: "track-2", Title: "Album Artist", AlbumID: "album-1"},
			{ID: "track-3", Title: "Other", ArtistIDs: []string{"artist-2"}},
		},
	})

	tracks := service.MusicTracksForArtist("artist-1")
	if len(tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(tracks))
	}
}

func TestOverlayMusicAlbumUsesScopedTracksOnly(t *testing.T) {
	playedAt := time.Now().UTC()
	album := MusicAlbum{ID: "album-1", Title: "Mine"}
	otherAlbum := MusicAlbum{ID: "album-2", Title: "Other"}
	scopeTracks := []MusicTrack{
		{ID: "track-1", AlbumID: "album-1"},
		{ID: "track-2", AlbumID: "album-2"},
	}
	trackStates := map[string]PlaybackState{
		"track-1": {PlayCount: 4, LastPlayedAt: &playedAt},
		"track-2": {PlayCount: 99},
	}

	items := []MusicAlbum{album, otherAlbum}
	OverlayMusicAlbums(items, nil, scopeTracks, trackStates)

	if items[0].Playback.PlayCount != 4 {
		t.Fatalf("album-1 playCount = %d, want 4", items[0].Playback.PlayCount)
	}
	if items[1].Playback.PlayCount != 99 {
		t.Fatalf("album-2 playCount = %d, want 99 from scoped rollup", items[1].Playback.PlayCount)
	}
}
