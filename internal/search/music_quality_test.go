package search

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func TestSearchMusicAlbumsIncludeEnrichedAudioQuality(t *testing.T) {
	service := New()
	service.Rebuild(catalog.Seed{
		MusicAlbums: []catalog.MusicAlbum{{
			ID:         "album-1",
			Title:      "Quiet Nights",
			TrackCount: 1,
		}},
		MusicTracks: []catalog.MusicTrack{{
			ID:      "track-1",
			AlbumID: "album-1",
			Title:   "Blue In Green",
			AudioFiles: []catalog.AudioFile{{
				Path:       "/music/1.flac",
				Codec:      "flac",
				BitDepth:   24,
				SampleRate: 192000,
			}},
		}},
	})

	results := service.SearchMusicText("quiet", catalog.PageRequest{Limit: 10})
	if len(results.Albums) != 1 {
		t.Fatalf("albums = %#v", results.Albums)
	}
	album := results.Albums[0]
	if !album.HiRes {
		t.Fatal("expected search album HiRes true")
	}
	if album.MaxBitDepth != 24 || album.MaxSampleRate != 192000 {
		t.Fatalf("quality = %d/%d, want 24/192000", album.MaxBitDepth, album.MaxSampleRate)
	}
}
