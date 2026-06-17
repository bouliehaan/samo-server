package files

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// mp3ByteForSeconds returns the byte offset of the MP3 frame whose playback time
// contains targetSeconds, plus that frame's exact start time in milliseconds.
//
// MP3 is self-framing: every frame carries its own header, so a decoder can begin
// at any frame boundary. The standard Xing VBR table only resolves to ~1% of the
// file (minutes, on a long audiobook), which is why ExoPlayer's seeks land
// 20-70s off mid-sentence. Parsing the actual frame headers gives frame-accurate
// (~26ms) positions instead. ok is false if the stream can't be parsed as MP3.
func mp3ByteForSeconds(path string, targetSeconds float64) (startByte int64, startMs int64, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<16)

	var pos int64
	// Skip a leading ID3v2 tag so we start on a real audio frame.
	if head, err := br.Peek(10); err == nil && string(head[:3]) == "ID3" {
		size := int64(head[6]&0x7f)<<21 | int64(head[7]&0x7f)<<14 | int64(head[8]&0x7f)<<7 | int64(head[9]&0x7f)
		total := 10 + size
		if head[5]&0x10 != 0 { // footer present
			total += 10
		}
		if _, err := io.CopyN(io.Discard, br, total); err != nil {
			return 0, 0, false
		}
		pos = total
	}

	var t float64
	lastByte, lastT := pos, 0.0
	for {
		h, err := br.Peek(4)
		if err != nil {
			break
		}
		spf, frameLen, dur, valid := mp3FrameInfo(h)
		if !valid {
			// Not a frame sync — skip one byte and resync.
			_, _ = io.CopyN(io.Discard, br, 1)
			pos++
			continue
		}
		_ = spf
		// This frame covers [t, t+dur). Return it once it reaches the target.
		if t+dur > targetSeconds {
			return pos, int64(t*1000 + 0.5), true
		}
		lastByte, lastT = pos, t
		t += dur
		if _, err := io.CopyN(io.Discard, br, frameLen); err != nil {
			break
		}
		pos += frameLen
	}
	return lastByte, int64(lastT*1000 + 0.5), true
}

// mp3FrameInfo decodes a 4-byte MPEG audio frame header (Layer III) into its
// samples-per-frame, byte length, and duration. valid is false for anything that
// isn't a well-formed Layer III frame sync.
func mp3FrameInfo(h []byte) (samplesPerFrame int, frameLen int64, durSeconds float64, valid bool) {
	if len(h) < 4 || h[0] != 0xFF || h[1]&0xE0 != 0xE0 {
		return 0, 0, 0, false
	}
	version, okV := mp3Version[(h[1]>>3)&3]
	if !okV || (h[1]>>1)&3 != 1 { // layer bits 01 == Layer III
		return 0, 0, 0, false
	}
	bitrateIdx := int((h[2] >> 4) & 0xF)
	sampleIdx := int((h[2] >> 2) & 3)
	padding := int64((h[2] >> 1) & 1)
	if bitrateIdx == 0 || bitrateIdx == 15 || sampleIdx == 3 {
		return 0, 0, 0, false
	}
	group := 1
	if version != 1 {
		group = 2
	}
	bitrateK := mp3BitrateL3[group][bitrateIdx]
	sampleRate := mp3SampleRate[version][sampleIdx]
	if bitrateK == 0 || sampleRate == 0 {
		return 0, 0, 0, false
	}
	spf, coeff := 1152, int64(144)
	if version != 1 { // MPEG-2 / 2.5 Layer III use half the samples
		spf, coeff = 576, 72
	}
	frameLen = coeff*int64(bitrateK)*1000/int64(sampleRate) + padding
	if frameLen < 4 {
		return 0, 0, 0, false
	}
	return spf, frameLen, float64(spf) / float64(sampleRate), true
}

var (
	mp3Version    = map[byte]float64{0: 2.5, 2: 2, 3: 1}
	mp3SampleRate = map[float64][]int{
		1:   {44100, 48000, 32000},
		2:   {22050, 24000, 16000},
		2.5: {11025, 12000, 8000},
	}
	// Layer III bitrate tables (kbps) keyed by version group (1 = MPEG-1,
	// 2 = MPEG-2/2.5).
	mp3BitrateL3 = map[int][]int{
		1: {0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0},
		2: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
	}
)

// isMP3 reports whether a file should use frame-accurate MP3 seeking. Other
// containers (M4B/MP4/AAC) carry exact sample tables, so the player seeks them
// correctly and they keep whole-file serving.
func isMP3(contentType, path string) bool {
	if strings.Contains(strings.ToLower(contentType), "mpeg") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(path), ".mp3")
}
