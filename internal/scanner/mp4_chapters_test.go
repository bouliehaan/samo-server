package scanner

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// chplEntry encodes one { uint64 start (100ns units), pascal-string title } record.
func chplEntry(startSeconds int, title string) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, uint64(startSeconds)*chplTimeUnitsPerSecond)
	out = append(out, byte(len(title)))
	return append(out, []byte(title)...)
}

// chplPayloadV1 builds a version-1 'chpl' payload: version+flags (4) + reserved
// (4) + chapter count (1) + entries — the layout ffmpeg/Apple actually write.
func chplPayloadV1(entries ...[]byte) []byte {
	payload := []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, byte(len(entries))}
	for _, entry := range entries {
		payload = append(payload, entry...)
	}
	return payload
}

func TestParseChplPayload(t *testing.T) {
	payload := chplPayloadV1(chplEntry(1, "Intro"), chplEntry(8, "Part 1"))

	chapters, err := parseChplPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(chapters))
	}
	if chapters[0].StartSeconds != 1 || chapters[1].StartSeconds != 8 {
		t.Fatalf("starts = %v,%v want 1,8", chapters[0].StartSeconds, chapters[1].StartSeconds)
	}
	if chapters[0].EndSeconds != 8 {
		t.Fatalf("first end = %v, want 8", chapters[0].EndSeconds)
	}
}

// TestParseChplPayloadVersion0 covers the version-0 layout, where the chapter
// count directly follows version+flags with no reserved field.
func TestParseChplPayloadVersion0(t *testing.T) {
	payload := []byte{0x00, 0x00, 0x00, 0x00, 0x01} // version 0 + flags + count=1
	payload = append(payload, chplEntry(3, "Only")...)

	chapters, err := parseChplPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 1 || chapters[0].StartSeconds != 3 {
		t.Fatalf("got %+v, want one chapter at 3s", chapters)
	}
}

func TestMp4ChaptersFromFileFindsChplInMoov(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.m4b")

	chplPayload := chplPayloadV1(chplEntry(528, "Ch 1")) // 8:48
	chpl := make([]byte, 8+len(chplPayload))
	binary.BigEndian.PutUint32(chpl[0:4], uint32(len(chpl)))
	copy(chpl[4:8], []byte("chpl"))
	copy(chpl[8:], chplPayload)

	udta := make([]byte, 8+len(chpl))
	binary.BigEndian.PutUint32(udta[0:4], uint32(len(udta)))
	copy(udta[4:8], []byte("udta"))
	copy(udta[8:], chpl)

	moov := make([]byte, 8+len(udta))
	binary.BigEndian.PutUint32(moov[0:4], uint32(len(moov)))
	copy(moov[4:8], []byte("moov"))
	copy(moov[8:], udta)

	ftyp := make([]byte, 12)
	binary.BigEndian.PutUint32(ftyp[0:4], 12)
	copy(ftyp[4:8], []byte("ftyp"))
	copy(ftyp[8:12], []byte("M4A "))

	data := append(append([]byte(nil), ftyp...), moov...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	chapters, err := mp4ChaptersFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 1 {
		t.Fatalf("chapters = %d, want 1", len(chapters))
	}
	if chapters[0].StartSeconds != 528 {
		t.Fatalf("start = %v, want 528 (8:48)", chapters[0].StartSeconds)
	}
}
