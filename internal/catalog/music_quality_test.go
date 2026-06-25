package catalog

import "testing"

func TestEnrichAlbumAudioQualityOnCatalogLoad(t *testing.T) {
	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{{ID: "album-1", Title: "Quiet Nights", TrackCount: 1}},
		MusicTracks: []MusicTrack{{
			ID:      "track-1",
			AlbumID: "album-1",
			AudioFiles: []AudioFile{{
				Path:       "/music/1.flac",
				Codec:      "flac",
				BitDepth:   24,
				SampleRate: 192000,
			}},
		}},
	})

	album, err := service.MusicAlbum("album-1")
	if err != nil {
		t.Fatal(err)
	}
	if !album.HiRes {
		t.Fatal("expected album.HiRes true")
	}
	if album.AudioQuality != "24/192" {
		t.Fatalf("audioQuality = %q, want 24/192", album.AudioQuality)
	}
}
