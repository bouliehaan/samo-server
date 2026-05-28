package subsonic

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func TestAudioSuffixUsesCodecNotTagContainer(t *testing.T) {
	file := catalog.AudioFile{
		Path:      "/music/1.flac",
		Container: "VORBIS",
		Codec:     "flac",
	}
	if got := audioSuffix(file); got != "flac" {
		t.Fatalf("suffix = %q, want flac", got)
	}
}

func TestAudioSuffixM4AUsesAAC(t *testing.T) {
	file := catalog.AudioFile{
		Path:      "/music/1.m4a",
		Container: "MP4",
		Codec:     "aac",
	}
	if got := audioSuffix(file); got != "aac" {
		t.Fatalf("suffix = %q, want aac", got)
	}
}
