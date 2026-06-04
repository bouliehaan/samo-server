package scanner

import (
	"log"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// mergeProbeInfo keeps native tags and fills technical fields from ffprobe.
func mergeProbeInfo(native, ff probeInfo, includeChapters bool) probeInfo {
	out := native
	af := out.AudioFile

	if ff.AudioFile.DurationSeconds > 0 {
		af.DurationSeconds = ff.AudioFile.DurationSeconds
	}
	// ffprobe is the only probe path with sub-second precision; prefer its
	// millisecond duration so book-global chapter offsets stay exact.
	if ff.AudioFile.DurationMs > 0 {
		af.DurationMs = ff.AudioFile.DurationMs
	}
	if ff.AudioFile.Bitrate > 0 {
		af.Bitrate = ff.AudioFile.Bitrate
	}
	if ff.AudioFile.SampleRate > 0 {
		af.SampleRate = ff.AudioFile.SampleRate
	}
	if ff.AudioFile.Channels > 0 {
		af.Channels = ff.AudioFile.Channels
	}
	if ff.AudioFile.ChannelLayout != "" {
		af.ChannelLayout = ff.AudioFile.ChannelLayout
	}
	if ff.AudioFile.BitDepth > 0 {
		af.BitDepth = ff.AudioFile.BitDepth
	}
	if ff.AudioFile.Codec != "" {
		// ffprobe stream codec is the authoritative source when present.
		af.Codec = ff.AudioFile.Codec
	}
	if ff.AudioFile.CodecProfile != "" {
		af.CodecProfile = ff.AudioFile.CodecProfile
	}
	if ff.AudioFile.Container != "" && strings.TrimSpace(af.Container) == "" {
		af.Container = ff.AudioFile.Container
	}
	out.AudioFile = finalizeAudioFile(af)

	out.Tags = mergeCatalogTags(native.Tags, ff.Tags)
	// Embedded ffprobe chapters match audible playback; OverDrive MediaMarkers
	// from native tag reads are often tens of seconds early on retail .m4b files.
	if includeChapters && len(ff.Chapters) > 0 {
		out.Chapters = ff.Chapters
	}
	if !out.HasEmbeddedCover {
		out.HasEmbeddedCover = ff.HasEmbeddedCover
	}
	return out
}

func mergeCatalogTags(native, ff catalog.Tags) catalog.Tags {
	if len(ff) == 0 {
		return native
	}
	if len(native) == 0 {
		return ff
	}
	out := catalog.Tags{}
	for key, values := range native {
		out[key] = append([]string(nil), values...)
	}
	for key, values := range ff {
		if len(out[key]) > 0 {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

func logFFprobeFallback(path string, reason string) {
	log.Printf("scanner: ffprobe fallback for %q (%s)", path, reason)
}
