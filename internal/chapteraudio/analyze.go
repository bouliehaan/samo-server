package chapteraudio

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Recommendation values the analyzer attaches to a Report.
const (
	RecommendApply    = "apply"    // confident enough to overwrite chapters
	RecommendReview   = "review"   // plausible but a human should look first
	RecommendFallback = "fallback" // not analyzable; keep existing chapters
)

// Params are the (few) tunable knobs. Everything ELSE the detector decides is
// derived from each file's own audio; these only bound how it's interpreted.
type Params struct {
	// MinGapSeconds: shortest silence kept as a gap. Small on purpose — the
	// clustering needs the in-chapter pauses present to learn what "normal" is.
	MinGapSeconds float64
	// MergeWithinSeconds: boundaries closer than this collapse into one (a
	// detected gap landing on a file boundary, say).
	MergeWithinSeconds float64
	// EdgeMarginSeconds: ignore boundaries this close to the very start or end of
	// the book (intro stinger / end-of-book outro silence), so they don't create
	// a sliver "chapter".
	EdgeMarginSeconds float64
	// NameSkipPenalty: cost (seconds-equivalent) the name aligner pays to leave a
	// metadata title unused. High enough that titles are placed unless there's
	// genuinely no nearby boundary.
	NameSkipPenalty float64
	// ApplyConfidence: minimum overall confidence to recommend "apply".
	ApplyConfidence float64
	// MinChapterRatio: a chapter pause must be at least this many times the
	// longest in-chapter pause to count as a chapter break. Below it, audio alone
	// can't tell a chapter break from a dramatic pause.
	MinChapterRatio float64
}

// DefaultParams returns sane starting values. The inspector can override them on
// the command line so we can sweep on real books before trusting auto-apply.
func DefaultParams() Params {
	return Params{
		MinGapSeconds:      0.25,
		MergeWithinSeconds: 1.5,
		EdgeMarginSeconds:  2.0,
		NameSkipPenalty:    60,
		ApplyConfidence:    0.6,
		MinChapterRatio:    1.6,
	}
}

// FileInput is one audiobook file to analyze, in playback order. DurationSec and
// StartOffset come from the catalog when available; if StartOffset is 0 for a
// non-first file the analyzer accumulates measured durations itself, so the
// inspector can also run on a bare list of paths.
type FileInput struct {
	Path        string
	DurationSec float64
	StartOffset float64
}

// FileAnalysis is the per-file diagnostic surfaced by the inspector.
type FileAnalysis struct {
	Path        string
	DurationSec float64
	StartOffset float64
	SilenceDB   float64
	FloorDB     float64
	SpeechDB    float64
	FlatGate    float64
	Separation  float64
	GapCount    int
	LongestGap  float64
	Err         string
}

// ProposedChapter is one chapter the analyzer would write.
type ProposedChapter struct {
	Index            int
	Title            string
	StartSec         float64
	EndSec           float64
	Named            bool // title came from metadata (vs synthesized "Chapter N")
	FromFileBoundary bool // boundary is a file split (vs a detected silence)
}

// Report is the full result of analyzing one book — everything the inspector
// prints and the scanner persists.
type Report struct {
	Files             []FileAnalysis
	Gaps              []Gap // book-global, every gap (for the histogram)
	SplitSeconds      float64
	Separation        float64
	Boundaries        []float64 // book-global chapter starts, excluding the implicit 0
	FileBoundaryCount int
	GapBoundaryCount  int
	Chapters          []ProposedChapter
	AudioCount        int
	MetadataCount     int
	CountMatched      bool
	Confidence        float64
	Recommendation    string
	DurationSec       float64
	Notes             []string
}

// Analyzer derives chapters from audio. Construct with NewAnalyzer; it's
// stateless and safe to reuse across books.
type Analyzer struct {
	FFmpegPath string
	Params     Params
}

func NewAnalyzer(ffmpegPath string) *Analyzer {
	return &Analyzer{FFmpegPath: ffmpegPath, Params: DefaultParams()}
}

// AnalyzeBook runs the full pipeline over a book's files and proposes a chapter
// list. meta supplies candidate names (embedded or Audnexus chapters); pass nil
// to get audio-only "Chapter N" titles. It never writes anything.
func (a *Analyzer) AnalyzeBook(ctx context.Context, files []FileInput, meta []catalog.AudioChapter) (*Report, error) {
	rep := &Report{MetadataCount: len(meta)}
	if len(files) == 0 {
		rep.Recommendation = RecommendFallback
		rep.Notes = append(rep.Notes, "no files to analyze")
		return rep, nil
	}

	var allGaps []Gap
	var fileStarts []float64 // offset of each file after the first (true chapter anchors)
	running := 0.0
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		offset := f.StartOffset
		if offset == 0 && i > 0 {
			offset = running
		}
		fa, gaps, derr := a.analyzeFile(ctx, f, offset)
		rep.Files = append(rep.Files, fa)
		if derr != nil {
			rep.Notes = append(rep.Notes, fmt.Sprintf("file %s: %v", filepath.Base(f.Path), derr))
			// Keep the timeline intact using the catalog duration if we have one.
			running = offset + f.DurationSec
			continue
		}
		allGaps = append(allGaps, offsetGaps(gaps, offset)...)
		if i > 0 {
			fileStarts = append(fileStarts, offset)
		}
		running = offset + fa.DurationSec
	}
	rep.DurationSec = running

	if len(allGaps) == 0 && len(fileStarts) == 0 {
		rep.Recommendation = RecommendFallback
		rep.Notes = append(rep.Notes, "no silences and no file boundaries detected")
		rep.Chapters = []ProposedChapter{a.wholeBookChapter(rep.DurationSec, meta)}
		rep.AudioCount = 1
		return rep, nil
	}

	sort.Slice(allGaps, func(i, j int) bool { return allGaps[i].StartSec < allGaps[j].StartSec })
	rep.Gaps = allGaps

	ratio := a.Params.MinChapterRatio
	if ratio <= 1 {
		ratio = 1.6
	}
	cluster := clusterChapterGaps(allGaps, ratio, len(meta), len(fileStarts))
	rep.SplitSeconds = cluster.SplitSeconds
	rep.Separation = cluster.Separation
	rep.CountMatched = cluster.CountMatched

	// Assemble boundary candidates: detected chapter-gap midpoints plus every
	// file split (publishers split multi-file books on chapter boundaries, so a
	// file start is a near-certain chapter start).
	var cands []boundaryCand
	for _, g := range cluster.ChapterGaps {
		cands = append(cands, boundaryCand{Time: g.MidSec(), FromFile: false})
	}
	for _, off := range fileStarts {
		cands = append(cands, boundaryCand{Time: off, FromFile: true})
	}
	merged := mergeBoundaries(cands, a.Params.MergeWithinSeconds, a.Params.EdgeMarginSeconds, rep.DurationSec)

	for _, b := range merged {
		rep.Boundaries = append(rep.Boundaries, b.Time)
		if b.FromFile {
			rep.FileBoundaryCount++
		} else {
			rep.GapBoundaryCount++
		}
	}

	// Build chapter segments and map names on.
	starts := append([]float64{0}, rep.Boundaries...)
	matches := alignNames(starts, meta, a.Params.NameSkipPenalty)
	rep.Chapters = make([]ProposedChapter, len(starts))
	for i, st := range starts {
		end := rep.DurationSec
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		title, named := titleFor(i, matches[i], meta)
		fromFile := i >= 1 && merged[i-1].FromFile
		rep.Chapters[i] = ProposedChapter{
			Index:            i + 1,
			Title:            title,
			StartSec:         st,
			EndSec:           end,
			Named:            named,
			FromFileBoundary: fromFile,
		}
	}
	rep.AudioCount = len(rep.Chapters)
	rep.Confidence = a.blendConfidence(cluster.Confidence, rep.FileBoundaryCount, rep.GapBoundaryCount)
	rep.Recommendation = a.recommend(rep)
	return rep, nil
}

func (a *Analyzer) analyzeFile(ctx context.Context, f FileInput, offset float64) (FileAnalysis, []Gap, error) {
	fa := FileAnalysis{Path: f.Path, DurationSec: f.DurationSec, StartOffset: offset}
	b := newFeatureBuilder(f.DurationSec)
	feed := func(samples []float32) error { b.add(samples); return nil }
	if err := streamPCM(ctx, a.FFmpegPath, f.Path, feed); err != nil {
		fa.Err = err.Error()
		return fa, nil, err
	}
	feats := b.finish()
	th := estimateThresholds(feats)
	gaps := findGaps(feats, th, a.Params.MinGapSeconds)

	if fa.DurationSec <= 0 {
		fa.DurationSec = feats.DurationSeconds()
	}
	fa.SilenceDB = th.SilenceDB
	fa.FloorDB = th.FloorDB
	fa.SpeechDB = th.SpeechDB
	fa.FlatGate = th.FlatGate
	fa.Separation = th.Separation
	fa.GapCount = len(gaps)
	for _, g := range gaps {
		if g.Duration > fa.LongestGap {
			fa.LongestGap = g.Duration
		}
	}
	return fa, gaps, nil
}

// blendConfidence pulls the gap-cluster confidence upward when boundaries come
// from reliable file splits. A one-file-per-chapter book may have no internal
// gaps (cluster confidence ~0) yet still be perfectly chaptered by its file
// layout — that should read as high confidence, not low.
func (a *Analyzer) blendConfidence(clusterConf float64, fileBoundaries, gapBoundaries int) float64 {
	total := fileBoundaries + gapBoundaries
	if total == 0 {
		return clusterConf
	}
	reliable := float64(fileBoundaries) / float64(total)
	return clampF(clusterConf+(1-clusterConf)*0.6*reliable, 0, 1)
}

func (a *Analyzer) recommend(rep *Report) string {
	if len(rep.Gaps) == 0 && rep.FileBoundaryCount == 0 {
		return RecommendFallback
	}
	if rep.AudioCount <= 1 {
		if rep.MetadataCount > 1 {
			rep.Notes = append(rep.Notes,
				"audio found no internal chapter breaks but metadata expects multiple — possible continuous/music-bed recording")
			return RecommendReview
		}
		return RecommendApply // genuinely a single-chapter work
	}
	if rep.Confidence >= a.Params.ApplyConfidence {
		return RecommendApply
	}
	rep.Notes = append(rep.Notes,
		fmt.Sprintf("confidence %.2f below apply threshold %.2f", rep.Confidence, a.Params.ApplyConfidence))
	return RecommendReview
}

func (a *Analyzer) wholeBookChapter(total float64, meta []catalog.AudioChapter) ProposedChapter {
	title := "Chapter 1"
	named := false
	if len(meta) > 0 {
		if t := meta[0].Title; t != "" {
			title, named = t, true
		}
	}
	if total <= 0 {
		total = 1
	}
	return ProposedChapter{Index: 1, Title: title, StartSec: 0, EndSec: total, Named: named}
}

// AsAudioChapters projects the proposal into the catalog chapter type the
// scanner persists.
func (r *Report) AsAudioChapters() []catalog.AudioChapter {
	out := make([]catalog.AudioChapter, 0, len(r.Chapters))
	for _, c := range r.Chapters {
		out = append(out, catalog.AudioChapter{
			Index:        c.Index,
			Title:        c.Title,
			StartSeconds: c.StartSec,
			EndSeconds:   c.EndSec,
		})
	}
	return out
}

// boundaryCand is a candidate chapter start before merge/dedup.
type boundaryCand struct {
	Time     float64
	FromFile bool
}

// mergeBoundaries sorts, trims edge boundaries, and collapses near-duplicates.
// When a detected gap and a file split coincide, the file split's exact offset
// wins (it's the true seam) and the merged boundary is marked file-derived.
func mergeBoundaries(cands []boundaryCand, within, edgeMargin, total float64) []boundaryCand {
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Time < cands[j].Time })
	margin := math.Max(within, edgeMargin)

	var out []boundaryCand
	for _, c := range cands {
		if c.Time < margin || (total > 0 && c.Time > total-margin) {
			continue
		}
		if len(out) > 0 && c.Time-out[len(out)-1].Time <= within {
			last := &out[len(out)-1]
			if c.FromFile && !last.FromFile {
				last.Time = c.Time
			}
			last.FromFile = last.FromFile || c.FromFile
			continue
		}
		out = append(out, c)
	}
	return out
}
