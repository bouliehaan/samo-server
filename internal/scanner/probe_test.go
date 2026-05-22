package scanner

import "testing"

func TestFFProbeResultNormalizesAudioMetadata(t *testing.T) {
	raw := ffprobeResult{
		Format: ffprobeFormat{
			FormatName: "flac",
			Duration:   "245.4",
			Size:       "12345",
			Tags: map[string]string{
				"TITLE":  "Signal One",
				"GENRE":  "Ambient; Archive",
				"ARTIST": "The Static",
			},
		},
		Streams: []ffprobeStream{{
			CodecType:     "audio",
			CodecName:     "flac",
			SampleRate:    "96000",
			Channels:      2,
			ChannelLayout: "stereo",
			BitsPerSample: 24,
		}},
		Chapters: []ffprobeChapter{{
			StartTime: "0.0",
			EndTime:   "30.2",
			Tags:      map[string]string{"title": "Opening"},
		}},
	}

	probe := raw.toProbeInfo("/tmp/signal.flac")
	if probe.AudioFile.DurationSeconds != 245 {
		t.Fatalf("duration = %d, want 245", probe.AudioFile.DurationSeconds)
	}
	if probe.AudioFile.SampleRate != 96000 {
		t.Fatalf("sample rate = %d, want 96000", probe.AudioFile.SampleRate)
	}
	if got := probe.AudioFile.MetadataFormats; len(got) != 1 || got[0] != "vorbis" {
		t.Fatalf("metadata formats = %#v, want vorbis", got)
	}
	if got := splitTag(probe.Tags, "genre"); len(got) != 2 || got[1] != "Archive" {
		t.Fatalf("genres = %#v, want Ambient and Archive", got)
	}
	if probe.Chapters[0].Title != "Opening" {
		t.Fatalf("chapter title = %q, want Opening", probe.Chapters[0].Title)
	}
}

func TestOverdriveMarkersBecomeChapters(t *testing.T) {
	tags := normalizeTags(map[string]string{
		"OverDrive MediaMarkers": `<Markers><Marker><Name>Opening</Name><Time>0:00.000</Time></Marker><Marker><Name>Chapter 1</Name><Time>0:45.000</Time></Marker></Markers>`,
	})

	chapters := overdriveChapters(tags)
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(chapters))
	}
	if chapters[0].EndSeconds != 45 {
		t.Fatalf("first chapter end = %d, want 45", chapters[0].EndSeconds)
	}
}
