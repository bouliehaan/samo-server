package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bouliehaan/samo-server/internal/chapteraudio"
	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/toolchain"
)

// runChaptersInspect is the `samo-server chapters-inspect` subcommand: the
// dry-run validation tool for audio-anchored chapters. Point it at an audiobook
// ID (resolved from the catalog DB, names taken from stored chapters) or a bare
// file/folder path (no DB, audio-only). It decodes the audio, prints exactly
// what the analyzer found — adaptive silence gate per file, the gap-duration
// distribution, the chosen chapter split, the proposed chapters with confidence
// — and writes NOTHING unless you pass --apply. This is how you tune on real
// books before trusting auto-apply.
func runChaptersInspect(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("chapters-inspect", flag.ContinueOnError)
	var (
		apply      = fs.Bool("apply", false, "write the proposed chapters back to the DB (audiobook ID only)")
		all        = fs.Bool("all", false, "force re-analysis of every audiobook (requires --apply to write)")
		asJSON     = fs.Bool("json", false, "emit the full report as JSON")
		minGap     = fs.Float64("min-gap", 0, "override min silence gap seconds (default 0.25)")
		minRatio   = fs.Float64("min-ratio", 0, "override min chapter/intra pause ratio (default 1.6)")
		confidence = fs.Float64("confidence", 0, "override apply confidence threshold (default 0.6)")
		noDrift    = fs.Bool("no-drift", false, "disable the per-file-onset drift correction (A/B vs the affine-only registration)")
		label      = fs.Bool("label", false, "record the TRUE chapter-start times for <audiobook-id> into the golden set")
		truth      = fs.Bool("truth", false, "score the stored chapters and the analyzer's placement for <audiobook-id> against the golden set")
		times      = fs.String("times", "", "with --label: comma-separated true chapter starts (seconds or H:MM:SS); a leading 0 is dropped")
		snap       = fs.Bool("snap", false, "with --label: refine each given time to the nearest detected silence start")
		goldenPath = fs.String("golden", "golden-chapters.json", "path to the golden-set JSON file")
	)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: samo-server chapters-inspect [flags] <audiobook-id | path>")
		fmt.Fprintln(os.Stderr, "       samo-server chapters-inspect --all [--apply] [flags]")
		fmt.Fprintln(os.Stderr, "       samo-server chapters-inspect --label <audiobook-id> --times \"0,57,18:39,...\" [--snap]")
		fmt.Fprintln(os.Stderr, "       samo-server chapters-inspect --truth <audiobook-id>")
		fs.PrintDefaults()
	}
	positionals, err := parseFlagsAroundTarget(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) > 1 {
		fmt.Fprintf(os.Stderr, "expected one <audiobook-id | path>, got %d: %v\n", len(positionals), positionals)
		return 2
	}

	cfg, err := config.LoadEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	tools, err := toolchain.Resolve(toolchain.Options{DataDir: cfg.DataDir})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ffmpeg/ffprobe: %v\n", err)
		return 1
	}

	params := chapteraudio.DefaultParams()
	if *minGap > 0 {
		params.MinGapSeconds = *minGap
	}
	if *minRatio > 0 {
		params.MinChapterRatio = *minRatio
	}
	if *confidence > 0 {
		params.ApplyConfidence = *confidence
	}
	params.DriftCorrection = !*noDrift

	// Bare-path mode: analyze audio directly, no DB, no metadata names.
	target := ""
	if len(positionals) == 1 {
		target = strings.TrimSpace(positionals[0])
	}
	if !*all && target != "" && pathExists(target) {
		return inspectPath(ctx, tools.FFmpeg, params, target, *asJSON)
	}

	// DB modes need the catalog. The schema already exists (the server created
	// it), so this inspection tool only opens a handle — it does not migrate.
	db, err := storage.Open(ctx, cfg.DBDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		return 1
	}
	defer db.Close()
	scan := scanner.NewWithOptions(db, scanner.Options{
		FFmpegPath:  tools.FFmpeg,
		FFprobePath: tools.FFprobe,
		// Same Audnexus-backed provider the server uses, so --all and single-book
		// inspection converge to the authoritative chapter count exactly as a real
		// scan would.
		ChapterProvider:          chapterProviderForConfig(cfg.MetadataProviders, cfg.AudibleRegion),
		DisableAudioChapterDrift: *noDrift,
	})

	if *label || *truth {
		if target == "" {
			fs.Usage()
			return 2
		}
		if *label {
			return runChapterLabel(ctx, db, scan, target, *times, *snap, *goldenPath)
		}
		return runChapterTruth(ctx, db, scan, target, *goldenPath)
	}

	if *all {
		if !*apply {
			fmt.Println("--all without --apply: nothing is written. Add --apply to re-analyze every book (force) and write Audnexus-anchored chapters where they converge.")
			return 0
		}
		analyzed, applied, err := scan.RunChapterAnalysisPass(ctx, scanner.ChapterPassForce)
		if err != nil {
			fmt.Fprintf(os.Stderr, "analysis pass: %v\n", err)
			return 1
		}
		fmt.Printf("Done. Analyzed %d book(s), applied audio chapters to %d.\n", analyzed, applied)
		return 0
	}

	if target == "" {
		fs.Usage()
		return 2
	}
	return inspectAudiobookID(ctx, scan, target, *asJSON, *apply)
}

// inspectAudiobookID runs the exact production analysis for one book — Audnexus
// supplies the authoritative count + names, the silence threshold converges to it,
// and boundaries land at silence starts — then prints what the server would write
// (and writes it with --apply). The bare tuning flags (min-gap/min-ratio/
// confidence) apply only to the audio-only bare-path mode, not here, because the
// count-driven path derives its strictness from the target rather than fixed knobs.
func inspectAudiobookID(ctx context.Context, scan *scanner.Scanner, id string, asJSON, apply bool) int {
	rep, files, asin, anchor, err := scan.AnalyzeAudiobookChapters(ctx, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze: %v\n", err)
		return 1
	}
	title, path := scan.AudiobookDisplay(ctx, id)

	if asJSON {
		emitJSON(rep)
	} else {
		printReport(title, path, rep.MetadataCount, rep)
	}

	if apply {
		wrote, err := scan.ApplyAudioChapterReport(ctx, id, rep, files, asin, anchor)
		if err != nil {
			fmt.Fprintf(os.Stderr, "apply: %v\n", err)
			return 1
		}
		switch {
		case wrote && rep.HardTarget && rep.Recommendation == chapteraudio.RecommendApply:
			fmt.Printf("\nApplied: wrote %d audio-anchored chapters (source %s, confidence %.2f).\n",
				rep.AudioCount, sourceLabel(rep), rep.Confidence)
		case wrote:
			fmt.Printf("\nApplied: audio could not converge (found %d vs target %d); wrote %d verified Audnexus chapters instead of file splits.\n",
				rep.AudioCount, rep.TargetCount, len(anchor))
		default:
			fmt.Printf("\nNot applied (recommendation: %s). Existing chapters kept; confidence/signature recorded.\n", rep.Recommendation)
		}
	}
	return 0
}

func inspectPath(ctx context.Context, ffmpeg string, params chapteraudio.Params, path string, asJSON bool) int {
	files, err := gatherAudioFiles(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no audio files under %q\n", path)
		return 1
	}
	inputs := make([]chapteraudio.FileInput, len(files))
	for i, f := range files {
		inputs[i] = chapteraudio.FileInput{Path: f}
	}
	analyzer := &chapteraudio.Analyzer{FFmpegPath: ffmpeg, Params: params}
	rep, err := analyzer.AnalyzeBook(ctx, inputs, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze: %v\n", err)
		return 1
	}
	if asJSON {
		emitJSON(rep)
	} else {
		printReport(filepath.Base(path), path, 0, rep)
	}
	return 0
}

// ---- report rendering ----

func printReport(title, path string, metaCount int, rep *chapteraudio.Report) {
	fmt.Printf("Book:  %s\n", title)
	if path != "" {
		fmt.Printf("Path:  %s\n", path)
	}
	fmt.Printf("Files: %d · Duration: %s · Stored chapters: %d\n\n",
		len(rep.Files), hms(rep.DurationSec), metaCount)

	fmt.Println("Per-file silence analysis (energy in dBFS; flatGate is the tonal/flat split):")
	fmt.Printf("  %-3s %-30s %9s %7s %7s %7s %7s %6s %8s\n",
		"#", "file", "duration", "floor", "gate", "speech", "flatGate", "gaps", "longest")
	for i, f := range rep.Files {
		name := truncate(filepath.Base(f.Path), 30)
		if f.Err != "" {
			fmt.Printf("  %-3d %-30s  ERROR: %s\n", i+1, name, f.Err)
			continue
		}
		fmt.Printf("  %-3d %-30s %9s %7.1f %7.1f %7.1f %7.2f %6d %7.1fs\n",
			i+1, name, hms(f.DurationSec), f.FloorDB, f.SilenceDB, f.SpeechDB, f.FlatGate, f.GapCount, f.LongestGap)
	}

	printGapHistogram(rep.Gaps, rep.SplitSeconds)

	if rep.SplitSeconds > 0 {
		fmt.Printf("\nSilence threshold: keep gaps >= %.2fs", rep.SplitSeconds)
	} else {
		fmt.Printf("\nSilence threshold: n/a (boundaries are file seams)")
	}
	if rep.GateOffsetDB > 0 {
		fmt.Printf("   (gate loosened +%.0f dB to reach the count)", rep.GateOffsetDB)
	}
	fmt.Printf("\nSeparation: %.2f   confidence: %.2f\n", rep.Separation, rep.Confidence)
	fmt.Printf("Boundaries: %d  (from silence: %d, from file seams: %d)\n",
		len(rep.Boundaries), rep.GapBoundaryCount, rep.FileBoundaryCount)
	if rep.HardTarget {
		status := "MATCHED — audio converged to the authoritative count"
		if !rep.CountMatched {
			status = "UNMATCHED — keeping existing chapters"
		}
		fmt.Printf("Target (Audnexus): %d chapters   achieved: %d   (%s)\n", rep.TargetCount, rep.AudioCount, status)
	} else {
		match := "no metadata to compare"
		if rep.MetadataCount > 0 {
			if rep.CountMatched {
				match = "MATCHED — audio agrees with metadata count"
			} else {
				match = "differs — audio count wins"
			}
		}
		fmt.Printf("Audio chapters: %d   vs stored metadata: %d   (%s)\n", rep.AudioCount, rep.MetadataCount, match)
	}
	fmt.Printf("Recommendation: %s\n", strings.ToUpper(rep.Recommendation))

	fmt.Println("\nProposed chapters (audio decides count + position; names borrowed from metadata):")
	fmt.Printf("  %-4s %10s %10s %6s  %s\n", "#", "start", "length", "src", "title")
	named, generic := 0, 0
	for _, c := range rep.Chapters {
		src := "gap"
		if c.FromFileBoundary {
			src = "file"
		}
		if c.Index == 1 {
			src = "-"
		}
		tag := ""
		if !c.Named {
			tag = "  (generic)"
			generic++
		} else {
			named++
		}
		fmt.Printf("  %-4d %10s %10s %6s  %s%s\n",
			c.Index, hms(c.StartSec), hms(c.EndSec-c.StartSec), src, c.Title, tag)
	}
	fmt.Printf("Names: %d from metadata, %d generic; metadata had %d\n", named, generic, metaCount)

	if len(rep.Notes) > 0 {
		fmt.Println("\nNotes:")
		for _, n := range rep.Notes {
			fmt.Printf("  - %s\n", n)
		}
	}
}

func printGapHistogram(gaps []chapteraudio.Gap, split float64) {
	if len(gaps) == 0 {
		return
	}
	type bucket struct {
		lo, hi float64
		label  string
		count  int
	}
	buckets := []bucket{
		{0, 0.5, "0.25-0.5s", 0},
		{0.5, 1, "0.5-1s", 0},
		{1, 2, "1-2s", 0},
		{2, 4, "2-4s", 0},
		{4, 8, "4-8s", 0},
		{8, 1e9, "8s+", 0},
	}
	max := 0
	for _, g := range gaps {
		for i := range buckets {
			if g.Duration >= buckets[i].lo && g.Duration < buckets[i].hi {
				buckets[i].count++
				if buckets[i].count > max {
					max = buckets[i].count
				}
				break
			}
		}
	}
	fmt.Printf("\nGap duration distribution (%d gaps; chapter split >= %.2fs):\n", len(gaps), split)
	for _, b := range buckets {
		bar := ""
		if max > 0 {
			bar = strings.Repeat("█", (b.count*40)/max)
		}
		marker := ""
		if b.count > 0 && b.hi > split && split < 1e9 {
			marker = "  <- chapter breaks"
		}
		fmt.Printf("  %-10s %-41s %5d%s\n", b.label, bar, b.count, marker)
	}
}

func emitJSON(rep *chapteraudio.Report) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
}

func sourceLabel(rep *chapteraudio.Report) string {
	for _, c := range rep.Chapters {
		if c.Named {
			return "audio-aligned"
		}
	}
	return "audio-detected"
}

// ---- helpers ----

// parseFlagsAroundTarget parses fs against args while allowing flags to appear
// AFTER the positional target. The documented invocations put the audiobook id
// first (`--label <id> --times ...`), but stdlib flag stops at the first
// positional and would silently ignore everything behind it — so keep
// re-parsing the tail, collecting positionals as they surface.
func parseFlagsAroundTarget(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, rest[0])
		args = rest[1:]
	}
}

func gatherAudioFiles(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{root}, nil
	}
	var files []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if isAudioExt(p) {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func isAudioExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".aac", ".aif", ".aiff", ".alac", ".flac", ".m4a", ".m4b", ".mp3", ".ogg", ".opus", ".wav", ".wma":
		return true
	}
	return false
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// hms formats a duration in seconds as H:MM:SS (or M:SS under an hour).
func hms(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds + 0.5)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
