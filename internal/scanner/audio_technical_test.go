package scanner

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func TestFinalizeAudioFileCorrectsCodecFromPath(t *testing.T) {
	file := finalizeAudioFile(catalog.AudioFile{
		Path:            "/music/album/track.flac",
		Codec:           "vorbis",
		DurationSeconds: 200,
		SizeBytes:       10_000_000,
	})
	if file.Codec != "flac" {
		t.Fatalf("codec = %q, want flac", file.Codec)
	}
	if file.Bitrate <= 0 {
		t.Fatalf("bitrate = %d, want estimate from size/duration", file.Bitrate)
	}
}

func TestFinalizeAudioFileKeepsValidOGGCodec(t *testing.T) {
	file := finalizeAudioFile(catalog.AudioFile{
		Path:  "/music/track.ogg",
		Codec: "vorbis",
	})
	if file.Codec != "vorbis" {
		t.Fatalf("codec = %q, want vorbis", file.Codec)
	}
}

func TestFinalizeAudioFileKeepsOggFlacCodec(t *testing.T) {
	file := finalizeAudioFile(catalog.AudioFile{
		Path:  "/music/track.ogg",
		Codec: "flac",
	})
	if file.Codec != "flac" {
		t.Fatalf("codec = %q, want flac", file.Codec)
	}
}
