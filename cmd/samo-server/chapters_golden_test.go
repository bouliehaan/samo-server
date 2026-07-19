package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/chapteraudio"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestParseClock(t *testing.T) {
	good := map[string]float64{
		"57":         57,
		"57.4":       57.4,
		"18:39":      18*60 + 39,
		"18:39.5":    18*60 + 39.5,
		"1:02:03":    3723,
		"1:02:03.25": 3723.25,
		"0":          0,
	}
	for in, want := range good {
		got, err := parseClock(in)
		if err != nil {
			t.Fatalf("parseClock(%q): %v", in, err)
		}
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("parseClock(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"1:99", "x", "1:2:3:4", "-5", "1:-2"} {
		if _, err := parseClock(bad); err == nil {
			t.Fatalf("parseClock(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestParseTimesListDropsLeadingZeroAndSorts(t *testing.T) {
	got, err := parseTimesList("18:39, 0, 57")
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{57, 18*60 + 39}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if _, err := parseTimesList("57,57"); err == nil {
		t.Fatal("duplicate times should be rejected")
	}
}

func TestSnapToSilences(t *testing.T) {
	gaps := []chapteraudio.Gap{
		{StartSec: 10.0}, {StartSec: 100.0}, {StartSec: 200.0},
	}
	got := snapToSilences([]float64{11.5, 99.0, 150.0}, gaps)
	want := []float64{10.0, 100.0, 150.0} // last has no silence within 3s -> raw
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("snap = %v, want %v", got, want)
		}
	}

	// Two labels contending for one silence: the closer wins, the other stays raw.
	got = snapToSilences([]float64{9.5, 10.4}, gaps[:1])
	if math.Abs(got[0]-9.5) > 1e-9 || math.Abs(got[1]-10.0) > 1e-9 {
		t.Fatalf("collision snap = %v, want [9.5 10.0]", got)
	}
}

func TestMonotoneMatch(t *testing.T) {
	golden := []float64{10, 20, 30}
	placed := []float64{11, 29}
	pairs := make([]int, len(golden))
	monotoneMatch(golden, placed, pairs)
	if pairs[0] != 0 || pairs[1] != 1 || pairs[2] != -1 {
		t.Fatalf("pairs = %v, want [0 1 -1]", pairs)
	}

	// A placed boundary minutes away must not be forced into a pair.
	pairs = make([]int, 1)
	monotoneMatch([]float64{10}, []float64{500}, pairs)
	if pairs[0] != -1 {
		t.Fatalf("far match should stay unpaired, got %v", pairs)
	}
}

func TestScorePlacementTallies(t *testing.T) {
	sum := scorePlacement("test", []float64{10.2, 20.0, 45.0}, []float64{10.0, 20.0, 30.0})
	if !sum.countMatched {
		t.Fatal("counts match, summary disagrees")
	}
	if sum.beyondClose != 1 { // 45 vs 30 is 15s off
		t.Fatalf("beyondClose = %d, want 1", sum.beyondClose)
	}
	if sum.missed != 0 {
		t.Fatalf("missed = %d, want 0", sum.missed)
	}
}

func TestGoldenRoundTripAndLegacyFlatFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "golden-chapters.json")

	set, err := loadGolden(path) // missing file -> empty set
	if err != nil || len(set.Books) != 0 {
		t.Fatalf("empty load: %v %v", set, err)
	}
	set.Books["book-1"] = goldenEntry{Title: "Eldest", Chapters: 3, Boundaries: []float64{57.4, 1122.9}}
	if err := saveGolden(path, set); err != nil {
		t.Fatal(err)
	}
	back, err := loadGolden(path)
	if err != nil {
		t.Fatal(err)
	}
	if e := back.Books["book-1"]; e.Title != "Eldest" || len(e.Boundaries) != 2 || e.Boundaries[0] != 57.4 {
		t.Fatalf("round trip mangled entry: %+v", e)
	}

	// A pre-wrapper flat {id: entry} file still loads.
	legacy := filepath.Join(dir, "legacy.json")
	if err := os.WriteFile(legacy, []byte(`{"book-2":{"chapters":2,"boundaries":[99.5]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	flat, err := loadGolden(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if e := flat.Books["book-2"]; len(e.Boundaries) != 1 || e.Boundaries[0] != 99.5 {
		t.Fatalf("legacy load failed: %+v", flat.Books)
	}
}

func TestStoredInteriorBoundariesPrefersMillisAndDropsFirst(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO libraries (id, name, kind, path) VALUES ('lib', 'Books', 'audiobook', '/books')`)
	mustExec(`INSERT INTO audiobooks (id, library_id, path) VALUES ('bk', 'lib', '/books/bk')`)
	// start_ms carries sub-second truth; start_seconds is the legacy whole-second column.
	mustExec(`INSERT INTO audiobook_chapters (id, audiobook_id, chapter_index, title, start_seconds, start_ms)
	          VALUES ('c1','bk',0,'One',0,0), ('c2','bk',1,'Two',57,57430), ('c3','bk',2,'Three',1122,0)`)

	got, err := storedInteriorBoundaries(ctx, db, "bk")
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{57.43, 1122}
	if len(got) != 2 || math.Abs(got[0]-want[0]) > 1e-9 || math.Abs(got[1]-want[1]) > 1e-9 {
		t.Fatalf("boundaries = %v, want %v", got, want)
	}

	// A book with no chapter rows scores as "nothing stored", not an error.
	empty, err := storedInteriorBoundaries(ctx, db, "missing")
	if err != nil || empty != nil {
		t.Fatalf("missing book: %v %v", empty, err)
	}
}
