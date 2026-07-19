package catalog

import (
	"fmt"
	"os"
	"testing"
)

// TestMusicPlaylistCoverImagesGridsFromPerTrackArt is the server half of the
// explo artwork fix. Untagged drops share ONE folder-derived album, so an
// album-wide cover made every playlist entry (and the tile) show the same
// image. Once each drop carries its OWN cover on the TRACK, a playlist of
// distinct drops must yield 4 distinct covers — the 2x2 grid input, and the
// per-row art every client resolves from track.images.
func TestMusicPlaylistCoverImagesGridsFromPerTrackArt(t *testing.T) {
	tracks := make([]MusicTrack, 4)
	for i := range tracks {
		tracks[i] = MusicTrack{
			ID:      fmt.Sprintf("track-%d", i),
			AlbumID: "album-lump", // all in the one folder-derived album...
			// ...but each drop carries its OWN cover on the track.
			Images: []Image{{ID: fmt.Sprintf("cover_%d", i), Path: writeTestImageFile(t, fmt.Sprintf("c%d.jpg", i))}},
		}
	}
	service := NewService(Seed{
		MusicPlaylists: []MusicPlaylist{{
			ID:       "pl-1",
			Name:     "Explore",
			TrackIDs: []string{"track-0", "track-1", "track-2", "track-3"},
		}},
		MusicAlbums: []MusicAlbum{{ID: "album-lump", Title: "Weekly-Exploration"}},
		MusicTracks: tracks,
	})

	images := service.MusicPlaylistCoverImages("pl-1")
	if len(images) != 4 {
		t.Fatalf("MusicPlaylistCoverImages = %d images, want 4 (2x2 grid input): %#v", len(images), images)
	}
	distinct := map[string]bool{}
	for _, img := range images {
		distinct[img.ID] = true
	}
	if len(distinct) != 4 {
		t.Fatalf("playlist auto-cover has %d distinct covers, want 4: %#v", len(distinct), images)
	}
}

// TestMusicPlaylistCoverImagesResolvesStalePathViaID is the decisive prod
// repro: explo drops share ONE lump album; each track carries its own cover
// override, but the stored local PATH may not exist at enrichment time (rotated
// cover-store entry, container path mismatch). resolvedImagesLocked's strict
// os.Stat then DROPS the image — even though its ID still resolves via the media
// endpoint exactly as the client renders it — so every track collapses onto the
// lump-album cover and the tile shows one cover on both platforms.
func TestMusicPlaylistCoverImagesResolvesStalePathViaID(t *testing.T) {
	tracks := make([]MusicTrack, 4)
	for i := range tracks {
		tracks[i] = MusicTrack{
			ID:      fmt.Sprintf("track-%d", i),
			AlbumID: "album-lump",
			Images:  []Image{{ID: fmt.Sprintf("cover_%d", i), Path: fmt.Sprintf("/nonexistent/cover_%d.jpg", i)}},
		}
	}
	service := NewService(Seed{
		MusicPlaylists: []MusicPlaylist{{
			ID:       "pl-1",
			Name:     "Explore",
			TrackIDs: []string{"track-0", "track-1", "track-2", "track-3"},
		}},
		MusicAlbums: []MusicAlbum{{ID: "album-lump", Title: "Weekly-Exploration"}},
		MusicTracks: tracks,
	})

	images := service.MusicPlaylistCoverImages("pl-1")
	distinct := map[string]bool{}
	for _, im := range images {
		distinct[im.ID] = true
	}
	if len(distinct) < 2 {
		t.Fatalf("stale-path covers collapsed to %d distinct — the strict os.Stat drop is the tile bug: %#v", len(distinct), images)
	}
}

// TestMusicPlaylistCoverImagesLumpedAlbumIsSingleCover pins the bug the
// per-track fix corrects: with no track art and one shared album cover, the
// playlist auto-cover collapses to a SINGLE image — which every row then borrows
// (the "all explo tracks show the same cover" report).
func TestMusicPlaylistCoverImagesLumpedAlbumIsSingleCover(t *testing.T) {
	albumPath := writeTestImageFile(t, "album.jpg")
	tracks := make([]MusicTrack, 4)
	for i := range tracks {
		tracks[i] = MusicTrack{ID: fmt.Sprintf("track-%d", i), AlbumID: "album-lump"}
	}
	service := NewService(Seed{
		MusicPlaylists: []MusicPlaylist{{
			ID:       "pl-1",
			Name:     "Explore",
			TrackIDs: []string{"track-0", "track-1", "track-2", "track-3"},
		}},
		MusicAlbums: []MusicAlbum{{ID: "album-lump", Images: []Image{{ID: "cover_album", Path: albumPath}}}},
		MusicTracks: tracks,
	})

	images := service.MusicPlaylistCoverImages("pl-1")
	if len(images) != 1 {
		t.Fatalf("lumped-album playlist auto-cover = %d images, want 1 (the single-cover bug): %#v", len(images), images)
	}
}

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
