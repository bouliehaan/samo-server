package files

import (
	"os"
	"path/filepath"
	"testing"
)

// synthMP3 builds N identical MPEG-1 Layer III frames (64 kbps, 44.1 kHz, no
// padding -> 208 bytes/frame, 1152 samples/frame). Header FF FB 50 00.
func synthMP3(frames int) []byte {
	hdr := []byte{0xFF, 0xFB, 0x50, 0x00}
	const frameLen = 208
	buf := make([]byte, 0, frames*frameLen)
	for i := 0; i < frames; i++ {
		f := make([]byte, frameLen)
		copy(f, hdr)
		buf = append(buf, f...)
	}
	return buf
}

func TestMp3ByteForSeconds(t *testing.T) {
	// 10-byte ID3v2 header + 50-byte tag body, then audio frames.
	id3 := make([]byte, 60)
	copy(id3, []byte("ID3"))
	id3[3] = 4  // version 2.4
	id3[9] = 50 // syncsafe size = 50
	const audioStart = int64(60)
	const frameLen = int64(208)
	const durPerFrame = 1152.0 / 44100.0

	full := append(id3, synthMP3(300)...)
	path := filepath.Join(t.TempDir(), "x.mp3")
	if err := os.WriteFile(path, full, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, target := range []float64{0.0, 0.5, 1.0, 3.0, 5.0} {
		wantFrame := int64(target / durPerFrame) // frame containing target
		b, ms, ok := mp3ByteForSeconds(path, target)
		if !ok {
			t.Fatalf("target %.2f: ok=false", target)
		}
		wantByte := audioStart + wantFrame*frameLen
		if b != wantByte {
			t.Errorf("target %.2fs: byte=%d want=%d", target, b, wantByte)
		}
		wantMs := int64(float64(wantFrame)*durPerFrame*1000 + 0.5)
		if ms < wantMs-1 || ms > wantMs+1 {
			t.Errorf("target %.2fs: startMs=%d want~%d", target, ms, wantMs)
		}
		// The returned frame must start at or before the target (never overshoot).
		if float64(ms)/1000 > target+1e-9 {
			t.Errorf("target %.2fs: overshot to %dms", target, ms)
		}
	}
}

func TestIsMP3(t *testing.T) {
	cases := []struct {
		ct, path string
		want     bool
	}{
		{"audio/mpeg", "/a/b.mp3", true},
		{"", "/a/b.MP3", true},
		{"audio/mp4", "/a/b.m4b", false},
		{"audio/aac", "/a/b.m4a", false},
	}
	for _, c := range cases {
		if got := isMP3(c.ct, c.path); got != c.want {
			t.Errorf("isMP3(%q,%q)=%v want %v", c.ct, c.path, got, c.want)
		}
	}
}
