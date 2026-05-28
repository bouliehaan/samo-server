package scanner

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const mp3ScanLimit = 256 << 10 // 256 KiB

// mp3DurationSeconds reads duration from a Xing/Info/VBRI header when present.
func mp3DurationSeconds(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	data := make([]byte, mp3ScanLimit)
	n, err := file.Read(data)
	if err != nil && err != io.EOF {
		return 0, err
	}
	data = data[:n]

	for offset := 0; offset+4 < len(data); offset++ {
		if data[offset] != 0xff || offset+4 >= len(data) {
			continue
		}
		if data[offset+1]&0xe0 != 0xe0 {
			continue
		}
		// MPEG1 layer3 frame header is 4 bytes; side info follows.
		version := (data[offset+1] >> 3) & 0x03
		layer := (data[offset+1] >> 1) & 0x03
		if layer != 0x01 {
			continue
		}
		bitrateIdx := (data[offset+2] >> 4) & 0x0f
		sampleIdx := (data[offset+2] >> 2) & 0x03
		if bitrateIdx == 0 || bitrateIdx == 0x0f || sampleIdx == 0x03 {
			continue
		}
		sideInfoLen := 32
		if version == 0x03 { // MPEG1
			sideInfoLen = 32
		} else {
			sideInfoLen = 17
		}
		tagStart := offset + 4 + sideInfoLen
		if tagStart+12 > len(data) {
			continue
		}
		tag := string(data[tagStart : tagStart+4])
		if tag != "Xing" && tag != "Info" && tag != "VBRI" {
			continue
		}
		if tag == "VBRI" {
			return parseVBRI(data[tagStart:])
		}
		return parseXing(data[tagStart:], version == 0x03, sampleIdx)
	}
	return 0, fmt.Errorf("no xing/vbri header")
}

func parseXing(payload []byte, mpeg1 bool, sampleIdx byte) (int, error) {
	if len(payload) < 16 {
		return 0, fmt.Errorf("short xing")
	}
	flags := binary.BigEndian.Uint32(payload[4:8])
	if flags&0x01 == 0 {
		return 0, fmt.Errorf("xing missing frames flag")
	}
	frames := binary.BigEndian.Uint32(payload[8:12])
	if frames == 0 {
		return 0, fmt.Errorf("zero frames")
	}
	sampleRate := mpegSampleRate(mpeg1, sampleIdx)
	if sampleRate == 0 {
		return 0, fmt.Errorf("unknown sample rate")
	}
	samplesPerFrame := 1152
	if !mpeg1 {
		samplesPerFrame = 576
	}
	duration := int((uint64(frames) * uint64(samplesPerFrame)) / uint64(sampleRate))
	if duration <= 0 {
		return 0, fmt.Errorf("zero duration")
	}
	return duration, nil
}

func parseVBRI(payload []byte) (int, error) {
	if len(payload) < 26 {
		return 0, fmt.Errorf("short vbri")
	}
	delay := binary.BigEndian.Uint16(payload[4:6])
	quality := binary.BigEndian.Uint16(payload[6:8])
	_ = delay
	_ = quality
	bytes := binary.BigEndian.Uint32(payload[18:22])
	frames := binary.BigEndian.Uint32(payload[22:26])
	if frames == 0 || bytes == 0 {
		return 0, fmt.Errorf("empty vbri")
	}
	// VBRI stores total bytes and frames; duration needs effective bitrate.
	bitrate := (int64(bytes) * 8) / int64(frames) // not quite; use frames * 1152 / sr if known
	_ = bitrate
	// Many VBRI files also include sample rate at offset 14.
	sampleRate := int(binary.BigEndian.Uint16(payload[14:16]))
	if sampleRate > 0 {
		duration := int((uint64(frames) * 1152) / uint64(sampleRate))
		if duration > 0 {
			return duration, nil
		}
	}
	return 0, fmt.Errorf("vbri without sample rate")
}

func mpegSampleRate(mpeg1 bool, index byte) int {
	if mpeg1 {
		switch index {
		case 0:
			return 44100
		case 1:
			return 48000
		case 2:
			return 32000
		}
		return 0
	}
	switch index {
	case 0:
		return 44100
	case 1:
		return 48000
	case 2:
		return 32000
	}
	return 0
}
