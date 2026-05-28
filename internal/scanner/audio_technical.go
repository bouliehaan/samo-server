package scanner

import (
	"path/filepath"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// probeNeedsTechnicalSupplement reports whether ffprobe should fill stream
// technical fields. Native/header probes often find duration but not bitrate,
// bit depth, or sample rate — clients need those for quality badges.
func probeNeedsTechnicalSupplement(info probeInfo) bool {
	af := info.AudioFile
	if af.DurationSeconds <= 0 {
		return true
	}
	if strings.TrimSpace(af.Codec) == "" {
		return true
	}
	if af.SampleRate <= 0 {
		return true
	}
	// Ogg is container-level in native probing; force ffprobe to disambiguate
	// stream codec (vorbis/opus/flac) so clients don't render stale codec labels.
	normalized := catalog.NormalizeAudioFile(af)
	if strings.EqualFold(filepath.Ext(af.Path), ".ogg") && normalized.Codec == "vorbis" {
		return true
	}
	if isLosslessPath(af.Path) {
		return af.BitDepth <= 0
	}
	return af.Bitrate <= 0
}

func isLosslessPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac", ".wav", ".aiff", ".aif", ".alac":
		return true
	default:
		return false
	}
}

func finalizeProbeInfo(info probeInfo) probeInfo {
	info.AudioFile = finalizeAudioFile(info.AudioFile)
	return info
}

func finalizeAudioFile(file catalog.AudioFile) catalog.AudioFile {
	file = catalog.NormalizeAudioFile(file)
	if file.Bitrate <= 0 && file.DurationSeconds > 0 && file.SizeBytes > 0 {
		file.Bitrate = estimateBitrate(file.SizeBytes, file.DurationSeconds)
	}
	return file
}

func estimateBitrate(sizeBytes int64, durationSeconds int) int {
	if sizeBytes <= 0 || durationSeconds <= 0 {
		return 0
	}
	return int(sizeBytes * 8 / int64(durationSeconds))
}
