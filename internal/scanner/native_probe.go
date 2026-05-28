package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/dhowden/tag"
)

const nativeProbeTimeout = 15 * time.Second

// probeNative reads tags and technical metadata directly from file headers.
// No ffprobe subprocess — fast, predictable, and safe on network mounts.
func probeNative(ctx context.Context, path string, includeChapters bool) (probeInfo, error) {
	if err := ctx.Err(); err != nil {
		return probeInfo{}, err
	}
	type result struct {
		info probeInfo
		err  error
	}
	done := make(chan result, 1)
	go func() {
		info, err := probeNativeFile(path, includeChapters)
		done <- result{info: info, err: err}
	}()
	select {
	case <-ctx.Done():
		return probeInfo{}, ctx.Err()
	case res := <-done:
		return res.info, res.err
	}
}

func probeNativeFile(path string, includeChapters bool) (probeInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return probeInfo{}, err
	}
	defer file.Close()

	meta, err := tag.ReadFrom(file)
	if err != nil {
		return probeInfo{}, fmt.Errorf("read tags: %w", err)
	}

	stat, statErr := os.Stat(path)
	var modifiedAt *time.Time
	var sizeBytes int64
	if statErr == nil {
		modified := stat.ModTime().UTC()
		modifiedAt = &modified
		sizeBytes = stat.Size()
	}

	tags := tagsFromNativeMetadata(meta)
	sampleRate, duration := nativeDuration(path, meta, tags, sizeBytes)
	bitrate := bitrateFromTags(tags)

	audioFile := catalog.AudioFile{
		Path:            path,
		FileName:        filepath.Base(path),
		Container:       containerFromPath(path),
		MimeType:        mimeTypeForPath(path),
		Codec:           codecFromNative(meta, path),
		MetadataFormats: metadataFormatsFromTagFormat(path, meta.Format(), tags),
		Bitrate:         bitrate,
		SampleRate:      sampleRate,
		DurationSeconds: duration,
		SizeBytes:       sizeBytes,
		ModifiedAt:      modifiedAt,
		Checksum:        fileChecksum(path, stat),
		EmbeddedTags:    tags,
	}

	chapters := []catalog.AudioChapter{}
	if includeChapters {
		chapters = overdriveChapters(tags)
	}

	return finalizeProbeInfo(probeInfo{
		AudioFile:        audioFile,
		Tags:             tags,
		Chapters:         chapters,
		HasEmbeddedCover: meta.Picture() != nil,
	}), nil
}

func tagsFromNativeMetadata(meta tag.Metadata) catalog.Tags {
	tags := catalog.Tags{}
	put := func(key, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key = normalizeTagKey(key)
		tags[key] = append(tags[key], value)
	}

	put("title", meta.Title())
	put("album", meta.Album())
	put("artist", meta.Artist())
	put("album_artist", meta.AlbumArtist())
	put("composer", meta.Composer())
	put("genre", meta.Genre())
	if year := meta.Year(); year > 0 {
		put("date", strconv.Itoa(year))
	}
	if track, total := meta.Track(); track > 0 {
		if total > 0 {
			put("tracknumber", fmt.Sprintf("%d/%d", track, total))
		} else {
			put("tracknumber", strconv.Itoa(track))
		}
	}
	if disc, total := meta.Disc(); disc > 0 {
		if total > 0 {
			put("discnumber", fmt.Sprintf("%d/%d", disc, total))
		} else {
			put("discnumber", strconv.Itoa(disc))
		}
	}
	put("comment", meta.Comment())
	put("lyrics", meta.Lyrics())

	for key, raw := range meta.Raw() {
		normalized := normalizeTagKey(fmt.Sprint(key))
		for _, value := range stringifyRawTagValue(raw) {
			put(normalized, value)
		}
	}
	return tags
}

func stringifyRawTagValue(raw any) []string {
	switch value := raw.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			return []string{value}
		}
	case []string:
		return cleanParts(value)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			out = append(out, stringifyRawTagValue(item)...)
		}
		return cleanParts(out)
	}
	return nil
}

func nativeDuration(path string, meta tag.Metadata, tags catalog.Tags, sizeBytes int64) (sampleRate, seconds int) {
	if seconds = durationFromTags(tags); seconds > 0 {
		return sampleRate, seconds
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac":
		rate, dur, err := flacStreamInfo(path)
		if err == nil {
			return rate, dur
		}
	case ".m4a", ".m4b":
		if dur, err := mp4DurationSeconds(path); err == nil && dur > 0 {
			return sampleRate, dur
		}
	case ".mp3":
		if dur, err := mp3DurationSeconds(path); err == nil && dur > 0 {
			return sampleRate, dur
		}
	}
	_ = meta
	_ = sizeBytes
	return 0, 0
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
	default:
		return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	}
}

func codecFromNative(meta tag.Metadata, path string) string {
	switch strings.ToUpper(string(meta.Format())) {
	case "FLAC":
		return "flac"
	case "MP3":
		return "mp3"
	case "MP4":
		return "aac"
	case "OGG":
		return "vorbis"
	default:
		switch strings.ToLower(filepath.Ext(path)) {
		case ".opus":
			return "opus"
		case ".wav":
			return "pcm_s16le"
		case ".aiff", ".aif":
			return "pcm_s16be"
		default:
			return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		}
	}
}

func bitrateFromTags(tags catalog.Tags) int {
	if value := firstTag(tags, "bitrate", "tbr"); value != "" {
		if n, err := strconv.Atoi(strings.Fields(value)[0]); err == nil && n > 0 {
			return n
		}
	}
	return 0
}
