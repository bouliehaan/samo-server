package scanner

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestParseChplPayload(t *testing.T) {
	payload := make([]byte, 4+8+1+5+8+1+6)
	copy(payload[4:4+8], []byte{0, 0, 0, 0, 0, 0, 3, 232}) // 1000 ms
	payload[12] = 5
	copy(payload[13:18], "Intro")
	copy(payload[18:18+8], []byte{0, 0, 0, 0, 0, 0, 31, 64}) // 8000 ms
	payload[26] = 6
	copy(payload[27:33], "Part 1")

	chapters, err := parseChplPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(chapters))
	}
	if chapters[0].StartSeconds != 1 || chapters[1].StartSeconds != 8 {
		t.Fatalf("starts = %d,%d want 1,8", chapters[0].StartSeconds, chapters[1].StartSeconds)
	}
	if chapters[0].EndSeconds != 8 {
		t.Fatalf("first end = %d, want 8", chapters[0].EndSeconds)
	}
}

func TestMp4ChaptersFromFileFindsChplInMoov(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.m4b")

	chplPayload := make([]byte, 4+8+1+4)
	binary.BigEndian.PutUint64(chplPayload[4:12], 528_000) // 8:48
	chplPayload[12] = 4
	copy(chplPayload[13:17], "Ch 1")
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
		t.Fatalf("start = %d, want 528 (8:48)", chapters[0].StartSeconds)
	}
}
