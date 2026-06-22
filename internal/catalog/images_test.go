package catalog

import (
	"os"
	"testing"
)

func TestImageByIDReturnsAlbumCover(t *testing.T) {
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{{
			ID:     "album-1",
			Title:  "Test Album",
			Images: []Image{{ID: "cover_abc123", Path: "/covers/album.jpg"}},
		}},
	})

	image, err := service.ImageByID("cover_abc123")
	if err != nil {
		t.Fatalf("ImageByID: %v", err)
	}
	if image.ID != "cover_abc123" {
		t.Fatalf("got id %q", image.ID)
	}
}

func TestEnrichAlbumImagesFromTracks(t *testing.T) {
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{{
			ID:    "album-1",
			Title: "Album",
		}},
		MusicTracks: []MusicTrack{{
			ID:      "track-1",
			Title:   "Track",
			AlbumID: "album-1",
			Images:  []Image{{ID: "cover_track", Path: "/covers/track.jpg"}},
		}},
	})

	album, err := service.MusicAlbum("album-1")
	if err != nil {
		t.Fatalf("MusicAlbum: %v", err)
	}
	if len(album.Images) != 1 || album.Images[0].ID != "cover_track" {
		t.Fatalf("album images not enriched from tracks: %#v", album.Images)
	}
}

func TestEnrichArtistImagesFromTrackBackedAlbum(t *testing.T) {
	service := NewService(Seed{
		MusicArtists: []MusicArtist{{ID: "artist-1", Name: "Artist"}},
		MusicAlbums: []MusicAlbum{{
			ID:             "album-1",
			Title:          "Album",
			AlbumArtistIDs: []string{"artist-1"},
		}},
		MusicTracks: []MusicTrack{{
			ID:      "track-1",
			Title:   "Track",
			AlbumID: "album-1",
			Images:  []Image{{ID: "cover_track", Path: "/covers/track.jpg"}},
		}},
	})

	artist, err := service.MusicArtist("artist-1")
	if err != nil {
		t.Fatalf("MusicArtist: %v", err)
	}
	if len(artist.Images) != 0 {
		t.Fatalf("album artist should not inherit track cover art: %#v", artist.Images)
	}
}
func TestBackfillMusicImagesFromExtractedCovers(t *testing.T) {
	service := NewService(Seed{
		MusicTracks: []MusicTrack{{
			ID:    "track-1",
			Title: "Track",
			AudioFiles: []AudioFile{{
				Path: "/music/album/song.flac",
			}},
		}},
		ExtractedCoversBySource: map[string]Image{
			"/music/album/song.flac": {
				ID:       "cover_backfill",
				Path:     "/covers/backfill.jpg",
				MimeType: "image/jpeg",
			},
		},
	})

	track, err := service.MusicTrack("track-1")
	if err != nil {
		t.Fatalf("MusicTrack: %v", err)
	}
	if len(track.Images) != 1 || track.Images[0].ID != "cover_backfill" {
		t.Fatalf("track images not backfilled: %#v", track.Images)
	}
}

func TestEnrichArtistImagesFromAlbums(t *testing.T) {
	service := NewService(Seed{
		MusicArtists: []MusicArtist{{ID: "artist-1", Name: "Artist"}},
		MusicAlbums: []MusicAlbum{{
			ID:             "album-1",
			Title:          "Album",
			AlbumArtistIDs: []string{"artist-1"},
			Images:         []Image{{ID: "cover_album", Path: "/covers/album.jpg"}},
		}},
	})

	artist, err := service.MusicArtist("artist-1")
	if err != nil {
		t.Fatalf("MusicArtist: %v", err)
	}
	if len(artist.Images) != 0 {
		t.Fatalf("artist should not inherit album cover art: %#v", artist.Images)
	}
}

func TestEnrichAlbumImagesFromExtractedCovers(t *testing.T) {
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{{
			ID:    "album-1",
			Title: "Album",
		}},
		MusicTracks: []MusicTrack{{
			ID:      "track-1",
			Title:   "Track",
			AlbumID: "album-1",
			AudioFiles: []AudioFile{{
				Path: "/music/album/song.flac",
			}},
		}},
		ExtractedCoversBySource: map[string]Image{
			"/music/album/song.flac": {
				ID:   "cover_extracted",
				Path: "/covers/extracted.jpg",
			},
		},
	})

	album, err := service.MusicAlbum("album-1")
	if err != nil {
		t.Fatalf("MusicAlbum: %v", err)
	}
	if len(album.Images) != 1 || album.Images[0].ID != "cover_extracted" {
		t.Fatalf("album images not enriched from extracted covers: %#v", album.Images)
	}
}

func TestEnrichArtistImagesFromTrackPerformerDoesNotInheritGuestAlbum(t *testing.T) {
	service := NewService(Seed{
		MusicArtists: []MusicArtist{
			{ID: "artist-va", Name: "Various Artists"},
			{ID: "artist-guest", Name: "Guest"},
		},
		MusicAlbums: []MusicAlbum{{
			ID:               "album-1",
			Title:            "Compilation",
			AlbumArtistIDs:   []string{"artist-va"},
			AlbumArtistNames: []string{"Various Artists"},
		}},
		MusicTracks: []MusicTrack{{
			ID:        "track-1",
			Title:     "Track",
			AlbumID:   "album-1",
			ArtistIDs: []string{"artist-guest"},
			Images:    []Image{{ID: "cover_track", Path: "/covers/track.jpg"}},
		}},
	})

	artist, err := service.MusicArtist("artist-guest")
	if err != nil {
		t.Fatalf("MusicArtist: %v", err)
	}
	if len(artist.Images) != 0 {
		t.Fatalf("guest artist should not inherit album art via track performer role: %#v", artist.Images)
	}
}

func TestMusicPlaylistCoverImagesFallsBackToAlbumArt(t *testing.T) {
	albumPath := writeTestImageFile(t, "album.jpg")
	service := NewService(Seed{
		MusicPlaylists: []MusicPlaylist{{
			ID:       "pl-1",
			Name:     "Mix",
			TrackIDs: []string{"track-1"},
			Images:   []Image{{ID: "stale_cover", Path: t.TempDir() + "/missing.jpg"}},
		}},
		MusicAlbums: []MusicAlbum{{
			ID:     "album-1",
			Title:  "Album",
			Images: []Image{{ID: "cover_album", Path: albumPath}},
		}},
		MusicTracks: []MusicTrack{{
			ID:      "track-1",
			AlbumID: "album-1",
		}},
	})

	images := service.MusicPlaylistCoverImages("pl-1")
	if len(images) != 1 || images[0].ID != "cover_album" {
		t.Fatalf("MusicPlaylistCoverImages = %#v, want album cover fallback", images)
	}
}

func TestMusicPlaylistCoverImagesKeepsURLWithoutTracks(t *testing.T) {
	service := NewService(Seed{
		MusicPlaylists: []MusicPlaylist{{
			ID:     "pl-1",
			Name:   "Mix",
			Images: []Image{{URL: "https://example.com/playlist-cover.jpg"}},
		}},
	})
	images := service.MusicPlaylistCoverImages("pl-1")
	if len(images) != 1 || images[0].URL != "https://example.com/playlist-cover.jpg" {
		t.Fatalf("MusicPlaylistCoverImages = %#v, want custom URL", images)
	}
}

func TestMusicPlaylistCoverImagesPrefersCustomUpload(t *testing.T) {
	albumPath := writeTestImageFile(t, "album.jpg")
	customPath := writeTestImageFile(t, "custom.jpg")
	service := NewService(Seed{
		MusicPlaylists: []MusicPlaylist{{
			ID:       "pl-1",
			Name:     "Mix",
			TrackIDs: []string{"track-1"},
			Images:   []Image{{ID: "custom", Path: customPath}},
		}},
		MusicAlbums: []MusicAlbum{{
			ID:     "album-1",
			Images: []Image{{ID: "cover_album", Path: albumPath}},
		}},
		MusicTracks: []MusicTrack{{
			ID:      "track-1",
			AlbumID: "album-1",
		}},
	})

	images := service.MusicPlaylistCoverImages("pl-1")
	if len(images) != 1 || images[0].ID != "custom" {
		t.Fatalf("MusicPlaylistCoverImages = %#v, want custom cover", images)
	}
}

func TestMusicPlaylistCoverImagesGathersUpTo4DistinctCovers(t *testing.T) {
	album1 := writeTestImageFile(t, "1.jpg")
	album2 := writeTestImageFile(t, "2.jpg")
	album3 := writeTestImageFile(t, "3.jpg")
	album4 := writeTestImageFile(t, "4.jpg")

	service := NewService(Seed{
		MusicPlaylists: []MusicPlaylist{{
			ID:       "pl-1",
			Name:     "Mix",
			TrackIDs: []string{"t1", "t2", "t3", "t3-dup", "t4", "t5"},
		}},
		MusicAlbums: []MusicAlbum{
			{ID: "a1", Images: []Image{{ID: "c1", Path: album1}}},
			{ID: "a2", Images: []Image{{ID: "c2", Path: album2}}},
			{ID: "a3", Images: []Image{{ID: "c3", Path: album3}}},
			{ID: "a4", Images: []Image{{ID: "c4", Path: album4}}},
		},
		MusicTracks: []MusicTrack{
			{ID: "t1", AlbumID: "a1"},
			{ID: "t2", AlbumID: "a2"},
			{ID: "t3", AlbumID: "a3"},
			{ID: "t3-dup", AlbumID: "a3"},
			{ID: "t4", AlbumID: "a4"},
			{ID: "t5", AlbumID: "a1"},
		},
	})

	images := service.MusicPlaylistCoverImages("pl-1")
	if len(images) != 4 {
		t.Fatalf("MusicPlaylistCoverImages length = %d, want 4", len(images))
	}
	if images[0].ID != "c1" || images[1].ID != "c2" || images[2].ID != "c3" || images[3].ID != "c4" {
		t.Fatalf("MusicPlaylistCoverImages = %#v, want c1, c2, c3, c4", images)
	}
}

func TestMusicPlaylistCoverImagesDuplicatesWhen2Or3(t *testing.T) {
	album1 := writeTestImageFile(t, "a1.jpg")
	album2 := writeTestImageFile(t, "a2.jpg")
	defer os.Remove(album1)
	defer os.Remove(album2)

	service := NewService(Seed{
		MusicPlaylists: []MusicPlaylist{{
			ID:       "pl-1",
			Name:     "Mix",
			TrackIDs: []string{"t1", "t2"},
		}},
		MusicAlbums: []MusicAlbum{
			{ID: "a1", Images: []Image{{ID: "c1", Path: album1}}},
			{ID: "a2", Images: []Image{{ID: "c2", Path: album2}}},
		},
		MusicTracks: []MusicTrack{
			{ID: "t1", AlbumID: "a1"},
			{ID: "t2", AlbumID: "a2"},
		},
	})

	images := service.MusicPlaylistCoverImages("pl-1")
	if len(images) != 4 {
		t.Fatalf("MusicPlaylistCoverImages length = %d, want 4", len(images))
	}
	if images[0].ID != "c1" || images[1].ID != "c2" || images[2].ID != "c1" || images[3].ID != "c2" {
		t.Fatalf("MusicPlaylistCoverImages = %v, want c1, c2, c1, c2", images)
	}
}

func writeTestImageFile(t *testing.T, name string) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMusicAlbumCoverImagesFallsBackToTrackArt(t *testing.T) {
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{{ID: "album-1", Title: "Album"}},
		MusicTracks: []MusicTrack{{
			ID:      "track-1",
			AlbumID: "album-1",
			Images:  []Image{{ID: "cover_track", Path: "/covers/track.jpg"}},
		}},
	})

	images := service.MusicAlbumCoverImages("album-1")
	if len(images) != 1 || images[0].ID != "cover_track" {
		t.Fatalf("MusicAlbumCoverImages = %#v", images)
	}
}
