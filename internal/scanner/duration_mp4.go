package scanner

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const mp4ProbeWindow = 8 << 20 // 8 MiB from start and end

// mp4DurationSeconds reads duration from an MP4/M4A/M4B mvhd atom.
func mp4DurationSeconds(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size < 32 {
		return 0, fmt.Errorf("file too small")
	}

	if seconds, err := mp4DurationInWindow(file, 0, min64(size, mp4ProbeWindow)); err == nil && seconds > 0 {
		return seconds, nil
	}
	if size > mp4ProbeWindow {
		start := size - mp4ProbeWindow
		if seconds, err := mp4DurationInWindow(file, start, size); err == nil && seconds > 0 {
			return seconds, nil
		}
	}
	return 0, fmt.Errorf("mvhd not found")
}

func mp4DurationInWindow(file *os.File, start, end int64) (int, error) {
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return 0, err
	}
	data := make([]byte, end-start)
	if _, err := io.ReadFull(file, data); err != nil {
		return 0, err
	}
	for offset := 0; offset+8 <= len(data); {
		atomSize := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if atomSize < 8 {
			break
		}
		name := string(data[offset+4 : offset+8])
		if offset+atomSize > len(data) {
			// Atom spans window boundary; still try mvhd if fully contained.
			if name == "mvhd" && offset+16 <= len(data) {
				if seconds, err := parseMVHD(data[offset+8:]); err == nil {
					return seconds, nil
				}
			}
			break
		}
		payload := data[offset+8 : offset+atomSize]
		switch name {
		case "mvhd":
			return parseMVHD(payload)
		case "moov", "trak", "mdia":
			if seconds, err := findMVHDInAtoms(payload); err == nil && seconds > 0 {
				return seconds, nil
			}
		}
		offset += atomSize
	}
	return 0, fmt.Errorf("mvhd not found")
}

func findMVHDInAtoms(data []byte) (int, error) {
	for offset := 0; offset+8 <= len(data); {
		atomSize := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if atomSize < 8 || offset+atomSize > len(data) {
			break
		}
		name := string(data[offset+4 : offset+8])
		payload := data[offset+8 : offset+atomSize]
		switch name {
		case "mvhd":
			return parseMVHD(payload)
		default:
			if seconds, err := findMVHDInAtoms(payload); err == nil && seconds > 0 {
				return seconds, err
			}
		}
		offset += atomSize
	}
	return 0, fmt.Errorf("mvhd not found")
}

func parseMVHD(payload []byte) (int, error) {
	if len(payload) < 20 {
		return 0, fmt.Errorf("short mvhd")
	}
	version := payload[0]
	switch version {
	case 0:
		if len(payload) < 24 {
			return 0, fmt.Errorf("short mvhd v0")
		}
		timescale := binary.BigEndian.Uint32(payload[12:16])
		duration := binary.BigEndian.Uint32(payload[16:20])
		return durationFromTimescale(timescale, uint64(duration))
	case 1:
		if len(payload) < 32 {
			return 0, fmt.Errorf("short mvhd v1")
		}
		timescale := binary.BigEndian.Uint32(payload[20:24])
		duration := binary.BigEndian.Uint64(payload[24:32])
		return durationFromTimescale(timescale, duration)
	default:
		return 0, fmt.Errorf("unsupported mvhd version %d", version)
	}
}

func durationFromTimescale(timescale uint32, duration uint64) (int, error) {
	if timescale == 0 || duration == 0 {
		return 0, fmt.Errorf("empty mvhd duration")
	}
	seconds := (duration + uint64(timescale/2)) / uint64(timescale)
	if seconds == 0 {
		return 0, fmt.Errorf("zero duration")
	}
	if seconds > 86400*24*30 { // 30 days — likely corrupt
		return 0, fmt.Errorf("unreasonable duration")
	}
	return int(seconds), nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
