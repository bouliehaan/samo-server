package catalog

import "testing"

func TestNormalizeAudioFileFlacShowsFlacNotVorbis(t *testing.T) {
	file := NormalizeAudioFile(AudioFile{
		Path:      "/music/track.flac",
		Container: "VORBIS",
		Codec:     "flac",
	})
	if file.Codec != "flac" {
		t.Fatalf("codec = %q, want flac", file.Codec)
	}
	if file.Container != "flac" {
		t.Fatalf("container = %q, want flac (not tag format VORBIS)", file.Container)
	}
}

func TestNormalizeAudioFileM4AShowsAACNotMP4(t *testing.T) {
	file := NormalizeAudioFile(AudioFile{
		Path:      "/music/track.m4a",
		Container: "MP4",
		Codec:     "aac",
	})
	if file.Container != "aac" {
		t.Fatalf("container = %q, want aac", file.Container)
	}
}

func TestNormalizeAudioFileMP3ShowsMP3NotID3(t *testing.T) {
	file := NormalizeAudioFile(AudioFile{
		Path:      "/music/track.mp3",
		Container: "ID3v2.4",
		Codec:     "mp3",
	})
	if file.Container != "mp3" {
		t.Fatalf("container = %q, want mp3", file.Container)
	}
}

func TestNormalizeAudioFileRepairsWrongCodecFromPath(t *testing.T) {
	file := NormalizeAudioFile(AudioFile{
		Path:      "/music/track.flac",
		Container: "VORBIS",
		Codec:     "vorbis",
	})
	if file.Codec != "flac" {
		t.Fatalf("codec = %q, want flac", file.Codec)
	}
}
