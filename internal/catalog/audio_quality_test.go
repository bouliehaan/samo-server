package catalog

import "testing"

func TestSummarizeAlbumAudioQualityHiRes(t *testing.T) {
	_, rate, quality, hiRes := summarizeAlbumAudioQuality([]MusicTrack{{
		AudioFiles: []AudioFile{{
			Path:       "/music/a.flac",
			Codec:      "flac",
			BitDepth:   24,
			SampleRate: 192000,
		}},
	}})
	if !hiRes {
		t.Fatal("expected hi-res album")
	}
	if quality != "24/192" {
		t.Fatalf("audioQuality = %q, want 24/192", quality)
	}
	if rate != 192000 {
		t.Fatalf("maxSampleRate = %d, want 192000", rate)
	}
}

func TestSummarizeAlbumAudioQualityCDNotHiRes(t *testing.T) {
	_, _, quality, hiRes := summarizeAlbumAudioQuality([]MusicTrack{{
		AudioFiles: []AudioFile{{
			Path:       "/music/a.flac",
			Codec:      "flac",
			BitDepth:   16,
			SampleRate: 44100,
		}},
	}})
	if hiRes {
		t.Fatal("16/44.1 should not be hi-res")
	}
	if quality != "" {
		t.Fatalf("audioQuality = %q, want empty", quality)
	}
}

func TestSummarizeAlbumAudioQualityUsesMaxAcrossTracks(t *testing.T) {
	depth, _, quality, hiRes := summarizeAlbumAudioQuality([]MusicTrack{
		{AudioFiles: []AudioFile{{Path: "/a.mp3", Codec: "mp3", BitDepth: 0, SampleRate: 44100, Bitrate: 320000}}},
		{AudioFiles: []AudioFile{{Path: "/b.flac", Codec: "flac", BitDepth: 24, SampleRate: 96000}}},
	})
	if !hiRes || quality != "24/96" {
		t.Fatalf("got depth=%d quality=%q hiRes=%v, want 24/96 true", depth, quality, hiRes)
	}
}
