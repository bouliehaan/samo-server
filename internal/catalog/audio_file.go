package catalog

import (
	"path/filepath"
	"strings"
)

// NormalizeAudioFile fixes codec/container fields for API clients.
// Historically container stored tag header types (ID3v2.4, VORBIS, MP4) from
// native tag readers; many clients display container as the audio format badge.
// We keep metadataFormats for tag schemes and align container with stream codec.
func NormalizeAudioFile(file AudioFile) AudioFile {
	want := codecFromPath(file.Path)
	if want != "" {
		if strings.TrimSpace(file.Codec) == "" || !codecMatchesExtension(file.Codec, file.Path) {
			file.Codec = want
		}
	}
	file.Codec = normalizeCodecName(file.Codec)
	if file.Codec != "" {
		file.Container = file.Codec
	} else if c := containerFromPath(file.Path); c != "" {
		file.Container = c
	} else {
		file.Container = normalizeLegacyContainer(file.Container, file.Path)
	}
	return file
}

// DisplayFormat returns the label clients should show for format (codec).
func DisplayFormat(file AudioFile) string {
	file = NormalizeAudioFile(file)
	if file.Codec != "" {
		return file.Codec
	}
	return file.Container
}

func codecFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return "mp3"
	case ".flac":
		return "flac"
	case ".m4a", ".m4b":
		return "aac"
	case ".opus":
		return "opus"
	case ".ogg":
		return "vorbis"
	case ".wav":
		return "pcm"
	case ".aiff", ".aif":
		return "pcm"
	case ".aac":
		return "aac"
	case ".wma":
		return "wma"
	case ".alac":
		return "alac"
	default:
		return ""
	}
}

func containerFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return "mp3"
	case ".flac":
		return "flac"
	case ".m4a", ".m4b":
		return "m4a"
	case ".opus":
		return "opus"
	case ".ogg":
		return "ogg"
	case ".wav":
		return "wav"
	case ".aiff", ".aif":
		return "aiff"
	case ".aac":
		return "aac"
	case ".wma":
		return "wma"
	case ".alac":
		return "alac"
	default:
		return ""
	}
}

func codecMatchesExtension(codec, path string) bool {
	normalized := normalizeCodecName(codec)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ogg":
		return normalized == "vorbis" || normalized == "opus" || normalized == "flac"
	default:
		want := codecFromPath(path)
		if want == "" {
			return true
		}
		return normalized == want
	}
}

func normalizeCodecName(codec string) string {
	codec = strings.ToLower(strings.TrimSpace(codec))
	switch codec {
	case "libmp3lame", "mp3float", "mp2", "mp2float":
		return "mp3"
	case "libvorbis", "vorbis":
		return "vorbis"
	case "libopus":
		return "opus"
	case "flac":
		return "flac"
	case "aac", "mp4a", "alac":
		return codec
	case "pcm_s16le", "pcm_s16be", "pcm_s24le", "pcm_f32le":
		return "pcm"
	default:
		return codec
	}
}

// normalizeLegacyContainer maps tag-format names still stored in old rows.
func normalizeLegacyContainer(container, path string) string {
	c := strings.ToLower(strings.TrimSpace(container))
	switch {
	case c == "":
		return containerFromPath(path)
	case strings.HasPrefix(c, "id3"):
		return "mp3"
	case c == "vorbis" && strings.EqualFold(filepath.Ext(path), ".flac"):
		return "flac"
	case c == "mp4" && (strings.EqualFold(filepath.Ext(path), ".m4a") || strings.EqualFold(filepath.Ext(path), ".m4b")):
		return "aac"
	case c == "flac", c == "mp3", c == "aac", c == "opus", c == "vorbis", c == "alac", c == "pcm":
		return c
	default:
		if fromPath := codecFromPath(path); fromPath != "" {
			return fromPath
		}
		return c
	}
}
