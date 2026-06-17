package catalog

import (
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var streamFirstNumber = regexp.MustCompile(`\d+`)

// StreamSelectQuery controls optional stream shortcut overrides.
type StreamSelectQuery struct {
	MediaFileID     string
	DiscNumber      int
	ProgressSeconds int
	// HasProgressSeconds is true when the client sent at/offsetSeconds/progressSeconds
	// (including an explicit zero to restart from the beginning).
	HasProgressSeconds bool
}

// StreamTarget is the selected media file and offset within that file.
type StreamTarget struct {
	FileID        string `json:"mediaFileId"`
	OffsetSeconds int    `json:"offsetSeconds"`
	GlobalSeconds int    `json:"globalSeconds,omitempty"`
}

// AudiobookStreamTarget is the file an audiobook stream should serve. Unlike
// StreamTarget it carries no within-file byte offset — audiobook files are
// always served whole and the client seeks locally. StartOffsetMs is the file's
// position on the book-global timeline so the client can map player-time to
// book-time.
type AudiobookStreamTarget struct {
	FileID        string
	StartOffsetMs int64
}

// SelectAudiobookFile picks which underlying file an audiobook stream should
// serve, without ever computing a within-file offset. Order of precedence:
//
//  1. explicit mediaFileId (the client knows exactly which file it wants)
//  2. the file whose book-global span contains progressSeconds (resume / a
//     book-time seek that crossed a file boundary)
//  3. the first file
//
// The whole file is then served with HTTP range support; the player seeks.
func SelectAudiobookFile(files []AudioFile, playback PlaybackState, query StreamSelectQuery) (AudiobookStreamTarget, error) {
	if len(files) == 0 {
		return AudiobookStreamTarget{}, fmt.Errorf("no audio files available")
	}
	sorted := assignStreamOffsets(SortAudioFiles(files))

	if query.MediaFileID != "" {
		for _, file := range sorted {
			if file.ID == query.MediaFileID {
				return AudiobookStreamTarget{FileID: file.ID, StartOffsetMs: fileStartOffsetMs(file)}, nil
			}
		}
		return AudiobookStreamTarget{}, fmt.Errorf("mediaFileId does not belong to this item")
	}

	progress := playback.ProgressSeconds
	if query.HasProgressSeconds {
		progress = query.ProgressSeconds
	}
	if progress > 0 {
		if file, ok := audiobookFileForBookSeconds(sorted, float64(progress)); ok {
			return AudiobookStreamTarget{FileID: file.ID, StartOffsetMs: fileStartOffsetMs(file)}, nil
		}
	}

	first := sorted[0]
	return AudiobookStreamTarget{FileID: first.ID, StartOffsetMs: fileStartOffsetMs(first)}, nil
}

// audiobookFileForBookSeconds returns the file whose [startOffset, startOffset
// + duration) span contains the given book-global second.
func audiobookFileForBookSeconds(sorted []AudioFile, bookSeconds float64) (AudioFile, bool) {
	for _, file := range sorted {
		start := file.StartOffsetSeconds
		end := start + audioFileDurationSeconds(file)
		if bookSeconds < end {
			return file, true
		}
	}
	if len(sorted) > 0 {
		return sorted[len(sorted)-1], true
	}
	return AudioFile{}, false
}

func fileStartOffsetMs(file AudioFile) int64 {
	if file.StartOffsetSeconds <= 0 {
		return 0
	}
	return int64(file.StartOffsetSeconds*1000 + 0.5)
}

// StreamSelectQueryFromRequest parses stream shortcut query parameters.
func StreamSelectQueryFromRequest(r *http.Request) StreamSelectQuery {
	if r == nil {
		return StreamSelectQuery{}
	}
	query := StreamSelectQuery{
		MediaFileID: strings.TrimSpace(r.URL.Query().Get("mediaFileId")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("disc")); raw != "" {
		if disc, err := strconv.Atoi(raw); err == nil && disc > 0 {
			query.DiscNumber = disc
		}
	}
	for _, key := range []string{"at", "offsetSeconds", "progressSeconds"} {
		if !r.URL.Query().Has(key) {
			continue
		}
		raw := strings.TrimSpace(r.URL.Query().Get(key))
		if raw == "" {
			continue
		}
		// Accept a fractional value: the client may send a sub-second seek
		// position (e.g. 502.982). File selection only needs whole-second
		// granularity, so truncate — but parsing must not reject the decimal.
		seconds, err := strconv.ParseFloat(raw, 64)
		if err != nil || seconds < 0 {
			continue
		}
		query.ProgressSeconds = int(seconds)
		query.HasProgressSeconds = true
		break
	}
	return query
}

// SelectStreamTarget picks the media file to stream for multi-part items.
func SelectStreamTarget(files []AudioFile, playback PlaybackState, query StreamSelectQuery, defaultDisc int) (StreamTarget, error) {
	if len(files) == 0 {
		return StreamTarget{}, fmt.Errorf("no audio files available")
	}

	sorted := SortAudioFiles(files)

	if query.MediaFileID != "" {
		for _, file := range sorted {
			if file.ID == query.MediaFileID {
				return StreamTarget{FileID: file.ID, OffsetSeconds: 0, GlobalSeconds: query.ProgressSeconds}, nil
			}
		}
		return StreamTarget{}, fmt.Errorf("mediaFileId does not belong to this item")
	}

	disc := query.DiscNumber
	if disc <= 0 {
		disc = defaultDisc
	}
	if disc > 0 {
		for _, file := range sorted {
			fileDisc, _ := fileDiscTrack(file)
			if fileDisc == disc {
				return StreamTarget{FileID: file.ID, OffsetSeconds: 0}, nil
			}
		}
	}

	progress := playback.ProgressSeconds
	if query.HasProgressSeconds {
		progress = query.ProgressSeconds
	}
	if progress <= 0 {
		return StreamTarget{FileID: sorted[0].ID, OffsetSeconds: 0}, nil
	}

	return streamTargetForProgress(sorted, progress)
}

func streamTargetForProgress(files []AudioFile, progressSeconds int) (StreamTarget, error) {
	offset := 0
	for _, file := range files {
		duration := file.DurationSeconds
		if duration <= 0 {
			continue
		}
		if progressSeconds < offset+duration {
			return StreamTarget{
				FileID:        file.ID,
				OffsetSeconds: progressSeconds - offset,
				GlobalSeconds: progressSeconds,
			}, nil
		}
		offset += duration
	}

	last := files[len(files)-1]
	return StreamTarget{FileID: last.ID, OffsetSeconds: 0, GlobalSeconds: progressSeconds}, nil
}

// SortAudioFiles orders linked files by disc and track metadata, then path.
func SortAudioFiles(files []AudioFile) []AudioFile {
	if len(files) <= 1 {
		return append([]AudioFile(nil), files...)
	}
	sorted := append([]AudioFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool {
		discI, trackI := fileDiscTrack(sorted[i])
		discJ, trackJ := fileDiscTrack(sorted[j])
		if discI != discJ {
			return discI < discJ
		}
		if trackI != trackJ {
			return trackI < trackJ
		}
		pathI := strings.ToLower(firstNonEmptyPath(sorted[i]))
		pathJ := strings.ToLower(firstNonEmptyPath(sorted[j]))
		return pathI < pathJ
	})
	return sorted
}

func fileDiscTrack(file AudioFile) (int, int) {
	disc, track := discTrackFromTags(file.EmbeddedTags)
	if disc == 0 {
		disc = filenameDiscNumber(firstNonEmptyPath(file))
	}
	if track == 0 {
		track = filenameTrackNumber(firstNonEmptyPath(file))
	}
	return disc, track
}

func discTrackFromTags(tags Tags) (int, int) {
	if len(tags) == 0 {
		return 0, 0
	}
	disc, _ := parseNumberPair(firstEmbeddedTag(tags, "discnumber", "disc", "disk", "tpos"))
	track, _ := parseNumberPair(firstEmbeddedTag(tags, "tracknumber", "track", "trck"))
	return disc, track
}

func firstEmbeddedTag(tags Tags, keys ...string) string {
	for _, key := range keys {
		normalized := normalizeStreamTagKey(key)
		values := tags[normalized]
		if len(values) == 0 {
			continue
		}
		if value := strings.TrimSpace(values[0]); value != "" {
			return value
		}
	}
	return ""
}

func normalizeStreamTagKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")
	return key
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

func firstNonEmptyPath(file AudioFile) string {
	if path := strings.TrimSpace(file.RelativePath); path != "" {
		return path
	}
	if path := strings.TrimSpace(file.Path); path != "" {
		return path
	}
	return strings.TrimSpace(file.FileName)
}

func filenameTrackNumber(path string) int {
	match := streamFirstNumber.FindString(filepath.Base(path))
	if match == "" {
		return 0
	}
	parsed, _ := strconv.Atoi(match)
	return parsed
}

func filenameDiscNumber(path string) int {
	matches := streamFirstNumber.FindAllString(filepath.Base(path), 2)
	if len(matches) < 2 {
		return 0
	}
	name := strings.ToLower(filepath.Base(path))
	if strings.Contains(name, "disc") || strings.Contains(name, "disk") || strings.Contains(name, "cd") {
		parsed, _ := strconv.Atoi(matches[0])
		return parsed
	}
	return 0
}
