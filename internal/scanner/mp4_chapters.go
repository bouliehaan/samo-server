package scanner

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// mp4ChaptersFromFile reads QuickTime chapter list (chpl) atoms from MP4/M4B.
// Retail audiobooks often store moov (and chpl) at the end of the file, so a
// small ffprobe -probesize window sees OverDrive tag markers but not embedded
// chapters. chpl matches what Apple Books and Audiobookshelf use for navigation.
func mp4ChaptersFromFile(path string) ([]catalog.AudioChapter, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".m4b" && ext != ".m4a" && ext != ".mp4" {
		return nil, fmt.Errorf("not mp4 family")
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size < 32 {
		return nil, fmt.Errorf("file too small")
	}

	windows := []struct{ start, end int64 }{
		{0, min64(size, mp4ProbeWindow)},
	}
	if size > mp4ProbeWindow {
		windows = append(windows, struct{ start, end int64 }{size - mp4ProbeWindow, size})
	}

	for _, window := range windows {
		chapters, err := mp4ChaptersInWindow(file, window.start, window.end)
		if err == nil && len(chapters) > 0 {
			return chapters, nil
		}
	}
	return nil, fmt.Errorf("chpl not found")
}

func mp4ChaptersInWindow(file *os.File, start, end int64) ([]catalog.AudioChapter, error) {
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data := make([]byte, end-start)
	if _, err := io.ReadFull(file, data); err != nil {
		return nil, err
	}
	payload, ok := findAtomPayload(data, "chpl")
	if !ok {
		return nil, fmt.Errorf("chpl not found")
	}
	return parseChplPayload(payload)
}

func findAtomPayload(data []byte, target string) ([]byte, bool) {
	for offset := 0; offset+8 <= len(data); {
		atomSize := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if atomSize < 8 {
			break
		}
		name := string(data[offset+4 : offset+8])
		if offset+atomSize > len(data) {
			break
		}
		payload := data[offset+8 : offset+atomSize]
		if name == target {
			return payload, true
		}
		if nested, ok := findAtomPayload(payload, target); ok {
			return nested, true
		}
		offset += atomSize
	}
	return nil, false
}

// chplTimeUnitsPerSecond is the Nero/QuickTime 'chpl' time base: chapter start
// times are stored in 100-nanosecond units (verified against ffmpeg-muxed m4b:
// a 20s chapter encodes as 200,000,000).
const chplTimeUnitsPerSecond = 10_000_000

func parseChplPayload(payload []byte) ([]catalog.AudioChapter, error) {
	if len(payload) < 5 {
		return nil, fmt.Errorf("short chpl")
	}
	// FullBox header: version (1 byte) + flags (3 bytes). For version != 0 a
	// 4-byte field follows (ffmpeg writes version 1), then a 1-byte chapter
	// count. Entries are then { uint64 start (100ns units), pascal-string title }.
	// (Matches libavformat mov_read_chpl; the prior offset=4 / milliseconds math
	// read the reserved+count bytes as the first timestamp and desynced the list.)
	version := payload[0]
	offset := 4
	if version != 0 {
		offset += 4
	}
	if offset >= len(payload) {
		return nil, fmt.Errorf("short chpl")
	}
	offset++ // chapter count byte; entries are length-delimited so we don't rely on it
	chapters := make([]catalog.AudioChapter, 0, 16)
	for offset+9 <= len(payload) {
		startUnits := binary.BigEndian.Uint64(payload[offset : offset+8])
		offset += 8
		titleLen := int(payload[offset])
		offset++
		if titleLen <= 0 || offset+titleLen > len(payload) {
			break
		}
		title := strings.TrimSpace(string(payload[offset : offset+titleLen]))
		offset += titleLen
		// 100ns units → fractional seconds; keep sub-second precision so chpl
		// chapter starts match the decoder exactly instead of rounding.
		startSeconds := float64(startUnits) / float64(chplTimeUnitsPerSecond)
		chapters = append(chapters, catalog.AudioChapter{
			Index:        len(chapters) + 1,
			Title:        firstNonEmpty(title, "Chapter "+strconv.Itoa(len(chapters)+1)),
			StartSeconds: startSeconds,
		})
	}
	if len(chapters) == 0 {
		return nil, fmt.Errorf("no chapters in chpl")
	}
	for i := 0; i < len(chapters)-1; i++ {
		chapters[i].EndSeconds = chapters[i+1].StartSeconds
	}
	last := &chapters[len(chapters)-1]
	if last.EndSeconds <= last.StartSeconds {
		last.EndSeconds = last.StartSeconds + 1
	}
	return chapters, nil
}
