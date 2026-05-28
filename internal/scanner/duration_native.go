package scanner

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// flacStreamInfo reads duration from the FLAC STREAMINFO metadata block.
func flacStreamInfo(path string) (sampleRate, durationSeconds int, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	var magic [4]byte
	if _, err := io.ReadFull(file, magic[:]); err != nil {
		return 0, 0, err
	}
	if string(magic[:]) != "fLaC" {
		return 0, 0, fmt.Errorf("not flac")
	}

	for {
		var header [4]byte
		if _, err := io.ReadFull(file, header[:]); err != nil {
			return 0, 0, err
		}
		last := header[0]&0x80 != 0
		blockType := header[0] & 0x7f
		length := int(header[1])<<16 | int(header[2])<<8 | int(header[3])
		if blockType == 0 {
			if length < 18 {
				return 0, 0, fmt.Errorf("short streaminfo")
			}
			data := make([]byte, length)
			if _, err := io.ReadFull(file, data); err != nil {
				return 0, 0, err
			}
			sampleRate = int((uint32(data[10])<<12)|(uint32(data[11])<<4)|uint32(data[12])>>4) & 0xfffff
			totalSamples := (uint64(data[13]&0x0f) << 32) | uint64(binary.BigEndian.Uint32(data[14:18]))
			if sampleRate > 0 && totalSamples > 0 {
				durationSeconds = int((totalSamples + uint64(sampleRate/2)) / uint64(sampleRate))
			}
			return sampleRate, durationSeconds, nil
		}
		if _, err := io.CopyN(io.Discard, file, int64(length)); err != nil {
			return 0, 0, err
		}
		if last {
			break
		}
	}
	return 0, 0, fmt.Errorf("no streaminfo")
}

func durationFromTags(tags catalog.Tags) int {
	for _, key := range []string{"length", "duration", "tlent", "tlen"} {
		if value := firstTag(tags, key); value != "" {
			if seconds := parseDurationTag(value); seconds > 0 {
				return seconds
			}
		}
	}
	return 0
}

func parseDurationTag(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if strings.Contains(value, ":") {
		parts := strings.Split(value, ":")
		seconds := 0
		for _, part := range parts {
			part = strings.TrimSpace(part)
			n, err := strconv.Atoi(part)
			if err != nil {
				return 0
			}
			seconds = seconds*60 + n
		}
		return seconds
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	// ID3 TLEN is milliseconds.
	if n > 1000 {
		return int((n + 500) / 1000)
	}
	return int(n)
}
