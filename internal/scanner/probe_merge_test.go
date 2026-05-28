package scanner

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func TestProbeNeedsTechnicalSupplement(t *testing.T) {
	if !probeNeedsTechnicalSupplement(probeInfo{AudioFile: catalog.AudioFile{}}) {
		t.Fatal("expected supplement when duration missing")
	}
	if !probeNeedsTechnicalSupplement(probeInfo{AudioFile: catalog.AudioFile{
		Path:            "/music/song.mp3",
		DurationSeconds: 180,
		Codec:           "mp3",
	}}) {
		t.Fatal("expected supplement when bitrate missing on lossy file")
	}
	if probeNeedsTechnicalSupplement(probeInfo{AudioFile: catalog.AudioFile{
		Path:            "/music/song.mp3",
		DurationSeconds: 180,
		Codec:           "mp3",
		Bitrate:         320000,
		SampleRate:      44100,
	}}) {
		t.Fatal("expected no supplement when technical fields present")
	}
}

func TestMergeProbeInfoKeepsNativeTags(t *testing.T) {
	native := probeInfo{
		Tags: catalog.Tags{"title": []string{"Native Title"}},
		AudioFile: catalog.AudioFile{
			DurationSeconds: 0,
			Codec:           "mp3",
		},
	}
	ff := probeInfo{
		Tags: catalog.Tags{"title": []string{"FF Title"}, "album": []string{"FF Album"}},
		AudioFile: catalog.AudioFile{
			DurationSeconds: 245,
			Bitrate:         320000,
			SampleRate:      44100,
		},
	}
	merged := mergeProbeInfo(native, ff, false)
	if firstTag(merged.Tags, "title") != "Native Title" {
		t.Fatalf("title = %q, want native", firstTag(merged.Tags, "title"))
	}
	if firstTag(merged.Tags, "album") != "FF Album" {
		t.Fatalf("album = %q", firstTag(merged.Tags, "album"))
	}
	if merged.AudioFile.DurationSeconds != 245 {
		t.Fatalf("duration = %d", merged.AudioFile.DurationSeconds)
	}
	if merged.AudioFile.Codec != "mp3" {
		t.Fatalf("codec = %q, want native mp3", merged.AudioFile.Codec)
	}
}

func TestMergeProbeInfoPrefersFFprobeCodec(t *testing.T) {
	native := probeInfo{
		AudioFile: catalog.AudioFile{
			Path:  "/music/song.ogg",
			Codec: "vorbis",
		},
	}
	ff := probeInfo{
		AudioFile: catalog.AudioFile{
			Path:  "/music/song.ogg",
			Codec: "flac",
		},
	}
	merged := mergeProbeInfo(native, ff, false)
	if merged.AudioFile.Codec != "flac" {
		t.Fatalf("codec = %q, want ffprobe flac", merged.AudioFile.Codec)
	}
}
