package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/chapteraudio"
	"github.com/bouliehaan/samo-server/internal/scanner"
)

// The golden set is the yardstick for the chapter-registration engine: a small
// JSON file of human-labeled TRUE chapter-start times for real books from the
// live library. `--label` records the truth (optionally snapped onto detected
// silence starts, since the human scrubs to roughly the right spot but the
// boundary belongs at the start of the held pause). `--truth` then scores two
// placements against it: the chapters currently stored in the DB (what users
// see today) and what the analyzer would place now — so every engine change
// gets an honest before/after number instead of a vibe.
//
// Times are stored as INTERIOR boundaries only (chapter 1 always starts at 0,
// so it carries no signal); a leading 0 in --times is accepted and dropped.
// This matches chapteraudio.Report.Boundaries, which uses the same convention.

// snapWindowSec bounds how far --snap may move a label to reach a detected
// silence start. Labels come from a human scrubbing audio, so they're within a
// couple of seconds of the truth; a match farther out is a different pause.
const snapWindowSec = 3.0

// Truth tolerances. Real chapter-boundary pauses run ~1–1.5s, so a placement
// within ±0.75s has landed in (or at) the right pause; within ±2.5s it found
// the neighborhood; beyond that it's simply wrong.
const (
	tolHit   = 0.75
	tolClose = 2.5
)

type goldenFile struct {
	Version int                    `json:"version"`
	Books   map[string]goldenEntry `json:"books"`
}

type goldenEntry struct {
	Title       string    `json:"title,omitempty"`
	ASIN        string    `json:"asin,omitempty"`
	Chapters    int       `json:"chapters"`   // len(Boundaries)+1, for human sanity
	Boundaries  []float64 `json:"boundaries"` // interior chapter starts, seconds, ascending
	DurationSec float64   `json:"durationSec,omitempty"`
	Snapped     bool      `json:"snapped,omitempty"`
	LabeledAt   string    `json:"labeledAt,omitempty"`
}

// runChapterLabel implements `chapters-inspect --label <id> --times ...`:
// parse the human-provided true starts, optionally snap them onto detected
// silence starts, persist them into the golden set, and immediately show how
// the analyzer's current placement scores against the fresh truth.
func runChapterLabel(ctx context.Context, db *sql.DB, scan *scanner.Scanner, id, timesCSV string, snap bool, goldenPath string) int {
	boundaries, err := parseTimesList(timesCSV)
	if err != nil {
		fmt.Fprintf(os.Stderr, "--times: %v\n", err)
		return 2
	}
	if len(boundaries) == 0 {
		fmt.Fprintln(os.Stderr, "--times: no interior boundaries given (chapter 1's start at 0 is implicit; label the starts of chapters 2..N)")
		return 2
	}

	// Run the real analysis up front: --snap needs the detected silences, and
	// even without it the fresh labels deserve an immediate score so the
	// labeling session doubles as a measurement.
	rep, _, asin, _, analyzeErr := scan.AnalyzeAudiobookChapters(ctx, id)
	if analyzeErr != nil {
		if snap {
			fmt.Fprintf(os.Stderr, "analyze (needed for --snap): %v\n", analyzeErr)
			return 1
		}
		fmt.Fprintf(os.Stderr, "warning: analysis failed (%v); saving raw labels without snap/validation or a score preview\n", analyzeErr)
	}

	if rep != nil && rep.DurationSec > 0 {
		if last := boundaries[len(boundaries)-1]; last > rep.DurationSec+2 {
			fmt.Fprintf(os.Stderr, "--times: %s is past the end of the book (%s)\n", clockf(last), clockf(rep.DurationSec))
			return 2
		}
	}

	if snap {
		boundaries = snapToSilences(boundaries, rep.Gaps)
	}

	title, _ := scan.AudiobookDisplay(ctx, id)
	entry := goldenEntry{
		Title:      title,
		ASIN:       asin,
		Chapters:   len(boundaries) + 1,
		Boundaries: boundaries,
		Snapped:    snap,
		LabeledAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if rep != nil {
		entry.DurationSec = rep.DurationSec
	}

	set, err := loadGolden(goldenPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load golden set: %v\n", err)
		return 1
	}
	if _, existed := set.Books[id]; existed {
		fmt.Printf("Replacing existing golden entry for %s.\n", id)
	}
	set.Books[id] = entry
	if err := saveGolden(goldenPath, set); err != nil {
		fmt.Fprintf(os.Stderr, "save golden set: %v\n", err)
		return 1
	}
	fmt.Printf("Labeled %q: %d chapters (%d interior boundaries) -> %s\n\n",
		title, entry.Chapters, len(boundaries), goldenPath)

	if rep != nil {
		fmt.Println("Score of the analyzer's CURRENT placement against these fresh labels:")
		scorePlacement("analyzer (proposed now)", rep.Boundaries, boundaries)
	}
	return 0
}

// runChapterTruth implements `chapters-inspect --truth <id>`: score both the
// stored (live) chapters and the analyzer's current proposal against the
// golden labels. Exit 0 only when the analyzer matched the boundary count and
// landed every boundary within the close tolerance — so A/B runs can be
// scripted (`--truth && echo pass`).
func runChapterTruth(ctx context.Context, db *sql.DB, scan *scanner.Scanner, id, goldenPath string) int {
	set, err := loadGolden(goldenPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load golden set: %v\n", err)
		return 1
	}
	entry, ok := set.Books[id]
	if !ok {
		fmt.Fprintf(os.Stderr, "no golden entry for %s in %s\n", id, goldenPath)
		if len(set.Books) > 0 {
			fmt.Fprintln(os.Stderr, "labeled books:")
			for bid, e := range set.Books {
				fmt.Fprintf(os.Stderr, "  %s  (%s, %d chapters)\n", bid, e.Title, e.Chapters)
			}
		} else {
			fmt.Fprintln(os.Stderr, "label one first: chapters-inspect --label <id> --times \"0,57,18:39,...\" [--snap]")
		}
		return 2
	}

	title, _ := scan.AudiobookDisplay(ctx, id)
	fmt.Printf("Book:   %s\n", title)
	fmt.Printf("Golden: %d chapters, labeled %s", entry.Chapters, entry.LabeledAt)
	if entry.Snapped {
		fmt.Printf(" (snapped to silence starts)")
	}
	fmt.Println()

	// What the users see today. Scored first so the analyzer number below has
	// its baseline right next to it.
	var source string
	var confidence float64
	_ = db.QueryRowContext(ctx,
		`SELECT COALESCE(chapter_source,''), COALESCE(chapter_confidence,0) FROM audiobooks WHERE id = ?`, id).
		Scan(&source, &confidence)
	stored, err := storedInteriorBoundaries(ctx, db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load stored chapters: %v\n", err)
		return 1
	}
	fmt.Printf("Stored: %d chapters (source %q, confidence %.2f)\n\n", len(stored)+1, source, confidence)
	scorePlacement("stored (live in DB)", stored, entry.Boundaries)

	rep, _, _, _, err := scan.AnalyzeAudiobookChapters(ctx, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze: %v\n", err)
		return 1
	}
	if rep.HardTarget {
		fmt.Printf("\nAnalyzer: target %d (Audnexus), achieved %d, confidence %.2f, recommendation %s\n",
			rep.TargetCount, rep.AudioCount, rep.Confidence, strings.ToUpper(rep.Recommendation))
	} else {
		fmt.Printf("\nAnalyzer: %d chapters (soft mode), confidence %.2f, recommendation %s\n",
			rep.AudioCount, rep.Confidence, strings.ToUpper(rep.Recommendation))
	}
	sum := scorePlacement("analyzer (proposed now)", rep.Boundaries, entry.Boundaries)

	if sum.countMatched && sum.missed == 0 && sum.beyondClose == 0 {
		fmt.Println("\nPASS: every golden boundary placed within the close tolerance.")
		return 0
	}
	fmt.Println("\nFAIL: see the ✗/MISS rows above.")
	return 3
}

// storedInteriorBoundaries returns the live DB chapters' interior start times,
// preferring the canonical integer-millisecond column over legacy whole
// seconds. The first chapter (start 0) is dropped to match the golden and
// Report.Boundaries convention.
func storedInteriorBoundaries(ctx context.Context, db *sql.DB, id string) ([]float64, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT start_seconds, start_ms FROM audiobook_chapters
		WHERE audiobook_id = ? ORDER BY chapter_index, start_ms, start_seconds`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var starts []float64
	for rows.Next() {
		var sec, ms int64
		if err := rows.Scan(&sec, &ms); err != nil {
			return nil, err
		}
		if ms > 0 {
			starts = append(starts, float64(ms)/1000)
		} else {
			starts = append(starts, float64(sec))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(starts) <= 1 {
		return nil, nil
	}
	return starts[1:], nil
}

// ---- scoring ----

type scoreSummary struct {
	countMatched bool
	missed       int
	beyondClose  int
}

// scorePlacement prints a per-boundary comparison of placed against golden and
// returns the tallies. When the counts match, boundaries pair by index — the
// honest reading, since the engine claims that exact chapter structure. On a
// mismatch it falls back to monotone nearest matching so the table still shows
// WHERE the placement diverged rather than one useless "count differs" line.
func scorePlacement(name string, placed, golden []float64) scoreSummary {
	sum := scoreSummary{countMatched: len(placed) == len(golden)}
	if len(placed) == 0 {
		fmt.Printf("  %s: no interior boundaries to score (%d golden)\n", name, len(golden))
		sum.missed = len(golden)
		return sum
	}

	pairs := make([]int, len(golden)) // golden index -> placed index, -1 = missed
	if sum.countMatched {
		for i := range golden {
			pairs[i] = i
		}
	} else {
		fmt.Printf("  %s: BOUNDARY COUNT MISMATCH — %d placed vs %d golden; matching monotonically\n",
			name, len(placed), len(golden))
		monotoneMatch(golden, placed, pairs)
	}

	fmt.Printf("  %s:\n", name)
	fmt.Printf("    %-4s %12s %12s %9s\n", "#", "golden", "placed", "delta")
	var absDeltas []float64
	for i, g := range golden {
		j := pairs[i]
		if j < 0 {
			fmt.Printf("    %-4d %12s %12s %9s  MISS\n", i+2, clockf(g), "-", "-")
			sum.missed++
			continue
		}
		d := placed[j] - g
		absDeltas = append(absDeltas, math.Abs(d))
		mark := "✓"
		switch {
		case math.Abs(d) > tolClose:
			mark = "✗"
			sum.beyondClose++
		case math.Abs(d) > tolHit:
			mark = "~"
		}
		fmt.Printf("    %-4d %12s %12s %+8.2fs  %s\n", i+2, clockf(g), clockf(placed[j]), d, mark)
	}
	if extra := len(placed) - (len(golden) - sum.missed); extra > 0 && !sum.countMatched {
		fmt.Printf("    (+%d placed boundaries matched no golden label)\n", extra)
	}

	if len(absDeltas) > 0 {
		sort.Float64s(absDeltas)
		within := func(tol float64) int {
			n := 0
			for _, d := range absDeltas {
				if d <= tol {
					n++
				}
			}
			return n
		}
		total := len(golden)
		fmt.Printf("    matched %d/%d · |Δ| mean %.2fs median %.2fs max %.2fs · ≤%.2gs %d/%d · ≤%.2gs %d/%d\n",
			len(absDeltas), total,
			mean(absDeltas), absDeltas[len(absDeltas)/2], absDeltas[len(absDeltas)-1],
			tolHit, within(tolHit), total,
			tolClose, within(tolClose), total)
	}
	return sum
}

// monotoneMatch pairs each golden boundary with the nearest not-yet-used
// placed boundary without ever crossing pairs (order-preserving), which is the
// only reading that makes sense for chapter sequences.
func monotoneMatch(golden, placed []float64, pairs []int) {
	j := 0
	for i, g := range golden {
		pairs[i] = -1
		if j >= len(placed) {
			continue
		}
		for j+1 < len(placed) && math.Abs(placed[j+1]-g) <= math.Abs(placed[j]-g) {
			j++
		}
		// Leave hopeless matches unpaired instead of forcing the table to
		// pretend a boundary minutes away corresponds to this chapter.
		if math.Abs(placed[j]-g) <= 60 {
			pairs[i] = j
			j++
		}
	}
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// ---- --snap ----

// snapToSilences moves each label to the START of the nearest detected silence
// within snapWindowSec ("the boundary lands in the held pause"). Labels stay
// raw — with a printed note — when no silence is near, and two labels never
// collapse onto the same silence (the closer one wins).
func snapToSilences(labels []float64, gaps []chapteraudio.Gap) []float64 {
	if len(gaps) == 0 {
		fmt.Println("--snap: analyzer detected no silences; keeping raw times")
		return labels
	}
	starts := make([]float64, len(gaps))
	for i, g := range gaps {
		starts[i] = g.StartSec
	}
	sort.Float64s(starts)

	type claim struct {
		label int
		dist  float64
	}
	claimed := map[int]claim{} // silence index -> winning label
	target := make([]int, len(labels))
	for i, t := range labels {
		target[i] = -1
		k := sort.SearchFloat64s(starts, t)
		for _, cand := range []int{k - 1, k} {
			if cand < 0 || cand >= len(starts) {
				continue
			}
			d := math.Abs(starts[cand] - t)
			if d > snapWindowSec {
				continue
			}
			if target[i] >= 0 && d >= math.Abs(starts[target[i]]-t) {
				continue
			}
			if prev, taken := claimed[cand]; taken && prev.dist <= d {
				continue
			}
			target[i] = cand
		}
		if target[i] >= 0 {
			if prev, taken := claimed[target[i]]; taken {
				fmt.Printf("--snap: #%d and #%d wanted the same silence; #%d keeps its raw time\n",
					prev.label+1, i+1, prev.label+1)
				target[prev.label] = -1
			}
			claimed[target[i]] = claim{label: i, dist: math.Abs(starts[target[i]] - t)}
		}
	}

	out := make([]float64, len(labels))
	for i, t := range labels {
		if target[i] < 0 {
			out[i] = t
			fmt.Printf("--snap: #%d %s — no silence start within %.0fs, kept raw\n", i+1, clockf(t), snapWindowSec)
			continue
		}
		out[i] = starts[target[i]]
		fmt.Printf("--snap: #%d %s -> %s (Δ%+.2fs)\n", i+1, clockf(t), clockf(out[i]), out[i]-t)
	}
	sort.Float64s(out)
	return out
}

// ---- parsing / persistence ----

// parseTimesList parses "0,57,18:39.5,1:02:03" into ascending interior
// boundary seconds. A leading 0 (chapter 1's implicit start) is dropped.
func parseTimesList(csv string) ([]float64, error) {
	var out []float64
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		t, err := parseClock(part)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	sort.Float64s(out)
	if len(out) > 0 && out[0] < 0.01 {
		out = out[1:]
	}
	for i := 1; i < len(out); i++ {
		if out[i]-out[i-1] < 0.01 {
			return nil, fmt.Errorf("duplicate time %s", clockf(out[i]))
		}
	}
	return out, nil
}

// parseClock accepts plain seconds ("57", "57.4") or clock forms
// ("18:39", "18:39.5", "1:02:03.25").
func parseClock(s string) (float64, error) {
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, fmt.Errorf("bad time %q (want seconds, M:SS, or H:MM:SS)", s)
	}
	total := 0.0
	for i, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("bad time %q (want seconds, M:SS, or H:MM:SS)", s)
		}
		if i > 0 && v >= 60 {
			return 0, fmt.Errorf("bad time %q: %v is not a valid minute/second field", s, p)
		}
		total = total*60 + v
	}
	return total, nil
}

// clockf renders seconds as H:MM:SS.s — one decimal, because sub-second
// placement is exactly what this tool measures.
func clockf(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	h := int(sec) / 3600
	m := (int(sec) % 3600) / 60
	rem := sec - float64(h*3600+m*60)
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%04.1f", h, m, rem)
	}
	return fmt.Sprintf("%d:%04.1f", m, rem)
}

func loadGolden(path string) (goldenFile, error) {
	set := goldenFile{Version: 1, Books: map[string]goldenEntry{}}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return set, nil
	}
	if err != nil {
		return set, err
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		return set, fmt.Errorf("%s: %w", path, err)
	}
	// Tolerate a flat {id: entry} file from before the versioned wrapper.
	if len(set.Books) == 0 {
		var flat map[string]goldenEntry
		if err := json.Unmarshal(raw, &flat); err == nil {
			for id, e := range flat {
				if len(e.Boundaries) > 0 {
					set.Books[id] = e
				}
			}
		}
	}
	if set.Books == nil {
		set.Books = map[string]goldenEntry{}
	}
	set.Version = 1
	return set, nil
}

// saveGolden writes atomically (temp + rename) so a crash mid-write can't eat
// the accumulated labels.
func saveGolden(path string, set goldenFile) error {
	blob, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), ".golden-tmp-"+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(tmp, append(blob, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
