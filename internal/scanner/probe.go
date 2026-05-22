package scanner

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jakedebus/samo-server/internal/catalog"
)

type ffprobeResult struct {
	Format   ffprobeFormat    `json:"format"`
	Streams  []ffprobeStream  `json:"streams"`
	Chapters []ffprobeChapter `json:"chapters"`
}

type ffprobeFormat struct {
	Filename   string            `json:"filename"`
	FormatName string            `json:"format_name"`
	Duration   string            `json:"duration"`
	Size       string            `json:"size"`
	BitRate    string            `json:"bit_rate"`
	Tags       map[string]string `json:"tags"`
}

type ffprobeStream struct {
	CodecType     string            `json:"codec_type"`
	CodecName     string            `json:"codec_name"`
	Profile       string            `json:"profile"`
	SampleRate    string            `json:"sample_rate"`
	Channels      int               `json:"channels"`
	ChannelLayout string            `json:"channel_layout"`
	BitsPerSample int               `json:"bits_per_sample"`
	BitsPerRaw    string            `json:"bits_per_raw_sample"`
	BitRate       string            `json:"bit_rate"`
	Tags          map[string]string `json:"tags"`
}

type ffprobeChapter struct {
	ID        int               `json:"id"`
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

type probeInfo struct {
	AudioFile catalog.AudioFile
	Tags      catalog.Tags
	Chapters  []catalog.AudioChapter
}

func (raw ffprobeResult) toProbeInfo(path string) probeInfo {
	audioStream := ffprobeStream{}
	for _, stream := range raw.Streams {
		if stream.CodecType == "audio" {
			audioStream = stream
			break
		}
	}

	tags := normalizeTags(raw.Format.Tags)
	for key, values := range normalizeTags(audioStream.Tags) {
		if _, ok := tags[key]; !ok {
			tags[key] = values
		}
	}

	stat, _ := os.Stat(path)
	var modifiedAt *time.Time
	var sizeBytes int64
	if stat != nil {
		modified := stat.ModTime().UTC()
		modifiedAt = &modified
		sizeBytes = stat.Size()
	}
	if parsed := parseInt64(raw.Format.Size); parsed > 0 {
		sizeBytes = parsed
	}

	duration := int(mathRound(parseFloat(raw.Format.Duration)))
	bitrate := int(parseInt64(audioStream.BitRate))
	if bitrate == 0 {
		bitrate = int(parseInt64(raw.Format.BitRate))
	}

	bitDepth := audioStream.BitsPerSample
	if bitDepth == 0 {
		bitDepth = int(parseInt64(audioStream.BitsPerRaw))
	}

	audioFile := catalog.AudioFile{
		Path:            path,
		FileName:        filepath.Base(path),
		Container:       raw.Format.FormatName,
		MimeType:        mimeTypeForPath(path),
		Codec:           audioStream.CodecName,
		CodecProfile:    audioStream.Profile,
		MetadataFormats: metadataFormatsForPath(path, tags),
		Bitrate:         bitrate,
		BitDepth:        bitDepth,
		SampleRate:      int(parseInt64(audioStream.SampleRate)),
		Channels:        audioStream.Channels,
		ChannelLayout:   audioStream.ChannelLayout,
		DurationSeconds: duration,
		SizeBytes:       sizeBytes,
		ModifiedAt:      modifiedAt,
		EmbeddedTags:    tags,
	}

	chapters := raw.chapters()
	if len(chapters) == 0 {
		chapters = overdriveChapters(tags)
	}

	return probeInfo{
		AudioFile: audioFile,
		Tags:      tags,
		Chapters:  chapters,
	}
}

func (raw ffprobeResult) chapters() []catalog.AudioChapter {
	chapters := make([]catalog.AudioChapter, 0, len(raw.Chapters))
	for index, chapter := range raw.Chapters {
		title := firstTag(normalizeTags(chapter.Tags), "title")
		if title == "" {
			title = "Chapter " + strconv.Itoa(index+1)
		}
		chapters = append(chapters, catalog.AudioChapter{
			Index:        index + 1,
			Title:        title,
			StartSeconds: int(mathRound(parseFloat(chapter.StartTime))),
			EndSeconds:   int(mathRound(parseFloat(chapter.EndTime))),
		})
	}
	return chapters
}

func normalizeTags(input map[string]string) catalog.Tags {
	tags := catalog.Tags{}
	for key, value := range input {
		key = normalizeTagKey(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		tags[key] = append(tags[key], value)
	}
	return tags
}

func normalizeTagKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")
	return key
}

func firstTag(tags catalog.Tags, keys ...string) string {
	for _, key := range keys {
		values := tags[normalizeTagKey(key)]
		if len(values) > 0 && strings.TrimSpace(values[0]) != "" {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

func splitTag(tags catalog.Tags, keys ...string) []string {
	value := firstTag(tags, keys...)
	if value == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ';' || r == '|' || r == '\\'
	})
	return cleanParts(parts)
}

func splitGenreTag(tags catalog.Tags, keys ...string) []string {
	value := firstTag(tags, keys...)
	if value == "" {
		return nil
	}
	value = strings.ReplaceAll(value, "//", "/")
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ';' || r == '|' || r == '/' || r == '\\'
	})
	return cleanParts(parts)
}

func cleanParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, part)
	}
	return out
}

func boolTag(tags catalog.Tags, keys ...string) bool {
	value := strings.ToLower(firstTag(tags, keys...))
	return value == "1" || value == "true" || value == "yes" || value == "y" || value == "explicit"
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return parsed
}

func mathRound(value float64) float64 {
	if value < 0 {
		return 0
	}
	return float64(int(value + 0.5))
}

func parseNumberPair(value string) (int, int) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0
	}
	parts := strings.SplitN(value, "/", 2)
	first, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	second := 0
	if len(parts) == 2 {
		second, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	}
	return first, second
}

func yearFromDate(value string) int {
	value = strings.TrimSpace(value)
	if len(value) < 4 {
		return 0
	}
	year, _ := strconv.Atoi(value[:4])
	return year
}

func mimeTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac":
		return "audio/flac"
	case ".m4a", ".m4b":
		return "audio/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".opus":
		return "audio/opus"
	case ".wav":
		return "audio/wav"
	default:
		return "application/octet-stream"
	}
}

func metadataFormatsForPath(path string, tags catalog.Tags) []string {
	if len(tags) == 0 {
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return []string{"id3"}
	case ".m4a", ".m4b":
		return []string{"mp4"}
	case ".flac", ".ogg", ".opus":
		return []string{"vorbis"}
	default:
		return []string{"embedded"}
	}
}
