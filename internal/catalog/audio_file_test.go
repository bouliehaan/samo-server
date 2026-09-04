package catalog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

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

// The raw tag dump is working data, not part of the API. It was 86% of
// /api/v1/podcasts on a real library, and no client has ever read it — the
// Android one deletes it on arrival.
func TestEmbeddedTagsAreNeverSerialized(t *testing.T) {
	file := AudioFile{
		ID:   "file_1",
		Path: "/podcasts/show/episode.mp3",
		EmbeddedTags: Tags{
			"comment": {strings.Repeat("a transcript nobody asked for ", 200)},
			"title":   {"An Episode"},
		},
	}

	encoded, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(encoded, []byte("embeddedTags")) {
		t.Error("embeddedTags was serialized")
	}
	if bytes.Contains(encoded, []byte("transcript nobody asked for")) {
		t.Errorf("tag values crossed the wire: %d bytes of JSON", len(encoded))
	}
	// The field is still there for the code that needs it.
	if got := file.EmbeddedTags["title"]; len(got) != 1 || got[0] != "An Episode" {
		t.Errorf("EmbeddedTags = %v, want it intact in memory", file.EmbeddedTags)
	}
}

// The one server-side reader of those tags. Dropping them from the JSON must
// not disturb how a multi-file audiobook is ordered.
func TestEmbeddedTagsStillOrderFiles(t *testing.T) {
	files := SortAudioFiles([]AudioFile{
		{ID: "b", Path: "/book/b.mp3", EmbeddedTags: Tags{"track": {"2"}}},
		{ID: "a", Path: "/book/a.mp3", EmbeddedTags: Tags{"track": {"1"}}},
	})
	if files[0].ID != "a" || files[1].ID != "b" {
		t.Errorf("order = %s,%s, want a,b", files[0].ID, files[1].ID)
	}
}
