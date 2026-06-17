package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/chapteraudio"
)

// audioChapterAnalyzerVersion is folded into every book's cache signature. Bump
// it whenever the detection algorithm changes so the next pass re-analyzes every
// book instead of trusting a result produced by the old logic.
//
// v5: registration with name-faithful placement + override-aware identification.
// Two changes over v4: (1) the book is identified from the metadata OVERRIDE the
// catalog/API serves (clean title + matched ASIN), not raw folder-name book_json —
// without it every Audible lookup searched the folder name and failed. (2) An
// AFFINE map (single-file edition / clean re-encode / gapless rip) places the
// Audnexus markers DIRECTLY at their warped positions — where Audible put them, at
// the chapter-name onset — instead of snapping to the nearest silence, which
// dragged boundaries seconds early off the name. Silence snapping is kept only for
// genuine multi-file rips with accumulating per-file drift, where the warp needs
// the silence anchors. v4 = affine+drift warp + per-chapter placement (snap).
const audioChapterAnalyzerVersion = "v6"

// AudioChapterAnalysisEnabled reports whether the scanner can run audio-anchored
// chapter analysis — i.e. an ffmpeg binary was configured to decode audio.
func (s *Scanner) AudioChapterAnalysisEnabled() bool {
	return strings.TrimSpace(s.ffmpegPath) != ""
}

// AnalyzeAudiobookChapters decodes one book and proposes a chapter list whose
// POSITIONS are derived entirely from the book's own audio (file seams and
// silences). The authoritative chapter COUNT and NAMES come from Audnexus for
// the verified edition: the detector assigns each expected break to the nearest
// seam/silence, loosening the silence gate until every break has an anchor.
// Because only Audnexus's count + titles + approximate positions are borrowed —
// never its timestamps as written values — the long-standing "markers drift the
// deeper you get" bug cannot recur.
//
// When no Audnexus edition resolves, the report is DIAGNOSTIC ONLY: the stored
// chapters are deliberately NOT used as a convergence target, because after any
// previous scan or analysis pass they are usually our own output (one chapter
// per file, or an old audio guess) — converging to them would launder yesterday's
// wrong answer into today's "match". That feedback loop is how a wrong count
// perpetuated itself; it is severed here.
//
// It writes NOTHING — ApplyAudioChapterReport persists it. Returns the report,
// the files analyzed (for the cache signature), the resolved ASIN (empty unless
// Audnexus anchored it), and the verified Audnexus anchor chapters themselves
// (nil unless an edition resolved). The anchor is returned so the apply step can
// write those named chapters directly when the audio convergence is not
// confident enough to refine them — a verified edition's markers always beat
// one-chapter-per-file track splits.
func (s *Scanner) AnalyzeAudiobookChapters(ctx context.Context, audiobookID string) (*chapteraudio.Report, []catalog.AudioFile, string, []catalog.AudioChapter, error) {
	if !s.AudioChapterAnalysisEnabled() {
		return nil, nil, "", nil, fmt.Errorf("audio chapter analysis unavailable: no ffmpeg configured")
	}
	files, err := catalog.AudiobookAudioFiles(ctx, s.db, audiobookID)
	if err != nil {
		return nil, nil, "", nil, err
	}
	if len(files) == 0 {
		return nil, nil, "", nil, fmt.Errorf("audiobook %s has no audio files on disk", audiobookID)
	}

	inputs := make([]chapteraudio.FileInput, 0, len(files))
	for _, f := range files {
		inputs = append(inputs, chapteraudio.FileInput{
			Path:        f.Path,
			DurationSec: audioFileAnalysisDuration(f),
			StartOffset: f.StartOffsetSeconds,
		})
	}

	var meta []catalog.AudioChapter
	asin := ""
	hard := false
	if s.chapterProvider != nil {
		label := s.audiobookLabel(ctx, audiobookID)
		lookup := s.audiobookChapterLookup(ctx, audiobookID, files)
		res := s.providerChaptersSafe(ctx, lookup)
		switch {
		case res.Outcome == ChapterApplied && len(res.Chapters) >= 2:
			meta, asin, hard = res.Chapters, res.ASIN, true
			log.Printf("scanner: audio chapters: %q — Audnexus target of %d chapter(s) (%s)",
				label, len(res.Chapters), res.Detail)
		default:
			// Loud and specific: this is the difference between "your chapters will
			// be fixed" and "we couldn't identify this book, fix its title/author
			// tags or set an asin tag". Detail carries the search scores.
			log.Printf("scanner: audio chapters: %q — NO Audnexus anchor (%s: %s; searched title=%q author=%q asin=%q); analysis is diagnostic only, nothing will be written",
				label, res.Outcome, res.Detail, lookup.Title, lookup.Author, lookup.ASIN)
		}
	}

	analyzer := chapteraudio.NewAnalyzer(s.ffmpegPath)
	// Audio is the position oracle: every boundary sits at the start of a real
	// silence (or an exact file seam), never at a metadata timestamp.
	analyzer.Params.BoundaryAtSilenceStart = true
	analyzer.Params.HardTargetCount = hard
	analyzer.Params.DriftCorrection = !s.audioChapterDriftOff

	rep, err := analyzer.AnalyzeBook(ctx, inputs, meta)
	if err != nil {
		return nil, files, asin, meta, err
	}
	return rep, files, asin, meta, nil
}

// audiobookChapterLookup builds the identifying info a ChapterProvider needs
// from the stored book row plus the on-disk runtime. Identification ladder:
// an ASIN embedded in the rip's tags is publisher ground truth and wins; then
// the ASIN a previous scan or pass already VERIFIED for this book
// (chapter_asin), so the pass can never disagree with the scan about which
// edition this is (and re-identification costs zero network calls); a verified
// title/author search is the last resort.
//
// The raw book_json holds the folder-derived title and usually no ASIN ("Eragon
// - Inheritance Book 01"); the clean title + matched ASIN live in a metadata
// override the catalog/API applies. We apply that SAME override here, or the
// lookup would search Audible for the folder name and find nothing — the exact
// reason a whole library can sit on file-boundary chapters despite Samo already
// knowing every book's ASIN.
func (s *Scanner) audiobookChapterLookup(ctx context.Context, audiobookID string, files []catalog.AudioFile) ChapterLookup {
	var bookJSON, verifiedASIN string
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(book_json,'{}'), COALESCE(chapter_asin,'') FROM audiobooks WHERE id = ?`,
		audiobookID).Scan(&bookJSON, &verifiedASIN)
	var book catalog.BookMetadata
	_ = json.Unmarshal([]byte(bookJSON), &book)
	if enriched, err := catalog.OverlayAudiobookOverride(ctx, s.db, catalog.AudiobookItem{ID: audiobookID, Book: &book}); err == nil && enriched.Book != nil {
		book = *enriched.Book
	}

	var total float64
	for _, f := range files {
		total += audioFileAnalysisDuration(f)
	}
	lookup := ChapterLookup{
		ASIN: firstNonEmpty(
			strings.TrimSpace(book.ExternalIDs.AudibleASIN),
			strings.TrimSpace(book.ExternalIDs.ASIN),
			strings.TrimSpace(verifiedASIN),
		),
		Title:           strings.TrimSpace(book.Title),
		DurationSeconds: total,
	}
	if len(book.Authors) > 0 {
		lookup.Author = strings.TrimSpace(book.Authors[0].Name)
	}
	return lookup
}

// audiobookLabel is a short human label for logs.
func (s *Scanner) audiobookLabel(ctx context.Context, audiobookID string) string {
	title, path := s.AudiobookDisplay(ctx, audiobookID)
	if title != "" {
		return title
	}
	if path != "" {
		return filepath.Base(path)
	}
	return audiobookID
}

// ApplyAudioChapterReport persists the analyzer's proposal along a strict quality
// ladder. The whole reason chapters were "all wrong" is that this used to be a
// single all-or-nothing gate: unless the AUDIO confidently reproduced the exact
// Audnexus count, the book fell all the way back to one-chapter-per-file — even
// when a verified Audible edition with correct, hand-named chapters was sitting
// right there. File-boundary chapters are the WORST answer; we never serve them
// when something better is in hand. The ladder, best to worst:
//
//  1. Audio-aligned convergence (HardTarget + RecommendApply): the audio matched
//     the verified count and placed every boundary on a real silence. Best — the
//     positions are measured from THIS file and cannot drift.
//  2. Raw Audnexus markers (anchor resolved but the audio could not confidently
//     converge): write the verified edition's named chapters directly, end-
//     anchored to the real runtime. Their offsets carry at most the small
//     edition-vs-file drift — a few seconds over many hours — which is vastly
//     better than track splits that are simply not chapters. Only ever REPLACES a
//     weak result (file/none/unverified audio guess); real in-file markers
//     (embedded/cue) and an already-good result are left untouched.
//  3. Heal to file truth: no verified edition at all → strip any stale v1/v2
//     audio-guess chapters back to the honest file-derived layout.
//
// Either way the confidence and cache signature are stamped. Returns whether
// chapters were rewritten.
func (s *Scanner) ApplyAudioChapterReport(ctx context.Context, audiobookID string, rep *chapteraudio.Report, files []catalog.AudioFile, asin string, anchor []catalog.AudioChapter) (bool, error) {
	sig := audioChapterSignature(files)

	// Rung 1: confident audio-aligned convergence — audio is the timing oracle.
	if rep != nil && rep.HardTarget && rep.Recommendation == chapteraudio.RecommendApply {
		chapters := fixChapterEndTimes(rep.AsAudioChapters(), rep.DurationSec)
		source := chapterSourceAudioAligned
		if !reportHasNames(rep) {
			source = chapterSourceAudioDetected
		}
		if err := s.replaceAudiobookChapters(ctx, audiobookID, chapters); err != nil {
			return false, err
		}
		now := time.Now().UTC()
		if err := s.setAudiobookChapterProvenance(ctx, audiobookID, source, asin, &now); err != nil {
			return false, err
		}
		if err := s.setAudioChapterMetrics(ctx, audiobookID, rep.Confidence, sig); err != nil {
			return false, err
		}
		return true, nil
	}

	// Rung 2: a verified Audnexus edition resolved but the audio could not
	// confidently refine it. Its named markers beat file-boundary chapters, so
	// write them rather than healing down to track splits — but never overwrite
	// real in-file markers (embedded/cue) or an already-good result to do so.
	if len(anchor) >= 2 {
		if cur := s.chapterSourceOf(ctx, audiobookID); chapterSourceIsWeak(cur) {
			total := bookDurationSeconds(files)
			if rep != nil && rep.DurationSec > 0 {
				total = rep.DurationSec
			}
			chapters := fixChapterEndTimes(anchor, total)
			if err := s.replaceAudiobookChapters(ctx, audiobookID, chapters); err != nil {
				return false, err
			}
			now := time.Now().UTC()
			if err := s.setAudiobookChapterProvenance(ctx, audiobookID, ChapterSourceAudnexus, asin, &now); err != nil {
				return false, err
			}
			conf := 0.0
			if rep != nil {
				conf = rep.Confidence
			}
			if err := s.setAudioChapterMetrics(ctx, audiobookID, conf, sig); err != nil {
				return false, err
			}
			return true, nil
		}
	}

	// Rung 3: no verified edition in hand. Heal stale audio-guess chapters back to
	// file truth and stamp metrics; leave anything authoritative alone.
	conf := 0.0
	if rep != nil {
		conf = rep.Confidence
	}
	if err := s.healAudioDerivedChapters(ctx, audiobookID, files); err != nil {
		return false, err
	}
	return false, s.setAudioChapterMetrics(ctx, audiobookID, conf, sig)
}

// chapterSourceOf reads a book's current chapter provenance label.
func (s *Scanner) chapterSourceOf(ctx context.Context, audiobookID string) string {
	var source string
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(chapter_source,'') FROM audiobooks WHERE id = ?`, audiobookID).Scan(&source)
	return source
}

// chapterSourceIsWeak reports whether a book's stored chapters are a weak,
// non-authoritative result that a better answer should replace: never analyzed,
// degenerate one-chapter-per-file, no chapters at all, or an unverified audio
// guess. Real in-file markers (embedded/cue), a verified Audnexus edition, and a
// confident audio-aligned result are all authoritative and NOT weak.
func chapterSourceIsWeak(source string) bool {
	switch strings.TrimSpace(source) {
	case "", chapterSourceFile, chapterSourceNone, chapterSourceAudioDetected:
		return true
	default:
		return false
	}
}

// ChapterPassScope controls which books a chapter analysis pass is allowed to
// (re)analyze. The decode is minutes per long book, so the scope is the contract
// that keeps routine events cheap: a server reboot runs NO pass at all, a quick
// scan touches only books whose files actually changed, and only a FULL scan —
// an explicit, user-initiated heavy operation — migrates the whole library onto
// a new analyzer version.
type ChapterPassScope int

const (
	// ChapterPassChanged analyzes only books never analyzed before or whose
	// files changed since their last analysis. Analyzer-version upgrades do NOT
	// make a book eligible. This is the after-quick-scan scope.
	ChapterPassChanged ChapterPassScope = iota
	// ChapterPassMigrate additionally re-analyzes books whose stored result was
	// produced by an older analyzer version. This is the after-full-scan scope.
	ChapterPassMigrate
	// ChapterPassForce re-analyzes everything regardless of signatures (manual
	// chapters-inspect --all).
	ChapterPassForce
)

func (sc ChapterPassScope) String() string {
	switch sc {
	case ChapterPassMigrate:
		return "migrate"
	case ChapterPassForce:
		return "force"
	default:
		return "changed-only"
	}
}

// RunChapterAnalysisPass analyzes the audiobooks the scope makes eligible and
// applies the proposals the analyzer is confident in. This is the decode-heavy
// pass — minutes per long book — so it is meant to run in the background AFTER a
// scan, never inline per file and never at boot. It skips ineligible books
// cheaply (signature compare, no decode) and honors ctx cancellation between
// books.
func (s *Scanner) RunChapterAnalysisPass(ctx context.Context, scope ChapterPassScope) (analyzed, applied int, err error) {
	if !s.AudioChapterAnalysisEnabled() {
		return 0, 0, nil
	}
	books, err := s.audiobooksForAnalysis(ctx)
	if err != nil {
		return 0, 0, err
	}
	log.Printf("scanner: audio chapter analysis pass over %d book(s) (scope=%s)", len(books), scope)

	for _, b := range books {
		if err := ctx.Err(); err != nil {
			return analyzed, applied, err
		}
		files, ferr := catalog.AudiobookAudioFiles(ctx, s.db, b.id)
		if ferr != nil {
			log.Printf("scanner: audio chapters: load files for %q failed: %v", b.label(), ferr)
			continue
		}
		if len(files) == 0 {
			continue
		}
		if !chapterAnalysisEligible(scope, b.sig, b.source, files) {
			continue // not in scope — skip the expensive decode
		}

		rep, _, asin, anchor, aerr := s.AnalyzeAudiobookChapters(ctx, b.id)
		if aerr != nil {
			log.Printf("scanner: audio chapters: analyze %q failed: %v", b.label(), aerr)
			continue
		}
		analyzed++
		didApply, werr := s.ApplyAudioChapterReport(ctx, b.id, rep, files, asin, anchor)
		if werr != nil {
			log.Printf("scanner: audio chapters: apply %q failed: %v", b.label(), werr)
			continue
		}
		switch {
		case didApply && rep != nil && rep.HardTarget && rep.Recommendation == chapteraudio.RecommendApply:
			applied++
			log.Printf("scanner: audio chapters APPLIED (audio-aligned) %q — %d chapters (target %d, %s), conf %.2f, cutoff %.2fs, gate +%.0f dB, source %s",
				b.label(), rep.AudioCount, rep.TargetCount, matchLabel(rep), rep.Confidence, rep.SplitSeconds, rep.GateOffsetDB, reportSource(rep))
		case didApply:
			applied++
			log.Printf("scanner: audio chapters APPLIED (Audnexus edition) %q — kept %d verified chapter(s); audio could not converge (found %d vs target %d), so the named markers beat file splits",
				b.label(), len(anchor), rep.AudioCount, rep.TargetCount)
		default:
			log.Printf("scanner: audio chapters KEPT existing for %q — %s (conf %.2f, audio=%d vs target=%d)",
				b.label(), rep.Recommendation, rep.Confidence, rep.AudioCount, rep.TargetCount)
		}
	}
	log.Printf("scanner: audio chapter analysis pass done — analyzed %d, applied %d", analyzed, applied)
	return analyzed, applied, nil
}

type analysisBook struct {
	id     string
	path   string
	sig    string
	source string
}

func (b analysisBook) label() string {
	if b.path != "" {
		return filepath.Base(b.path)
	}
	return b.id
}

func (s *Scanner) audiobooksForAnalysis(ctx context.Context) ([]analysisBook, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(path,''), COALESCE(chapter_audio_sig,''), COALESCE(chapter_source,'') FROM audiobooks`)
	if err != nil {
		return nil, fmt.Errorf("list audiobooks for analysis: %w", err)
	}
	defer rows.Close()
	var out []analysisBook
	for rows.Next() {
		var b analysisBook
		if err := rows.Scan(&b.id, &b.path, &b.sig, &b.source); err != nil {
			return nil, fmt.Errorf("scan audiobook row: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AudiobookDisplay returns a human label (title, falling back to folder name) and
// the on-disk path for one book — used by the inspector for readable output.
func (s *Scanner) AudiobookDisplay(ctx context.Context, audiobookID string) (title, path string) {
	var bookJSON string
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(path,''), COALESCE(book_json,'{}') FROM audiobooks WHERE id = ?`, audiobookID).
		Scan(&path, &bookJSON)
	var book struct {
		Title string `json:"title"`
	}
	_ = json.Unmarshal([]byte(bookJSON), &book)
	title = strings.TrimSpace(book.Title)
	if title == "" {
		title = filepath.Base(path)
	}
	return title, path
}

func reportHasNames(rep *chapteraudio.Report) bool {
	for _, c := range rep.Chapters {
		if c.Named {
			return true
		}
	}
	return false
}

func reportSource(rep *chapteraudio.Report) string {
	if reportHasNames(rep) {
		return chapterSourceAudioAligned
	}
	return chapterSourceAudioDetected
}

// matchLabel summarises whether the audio reached the authoritative count, for
// the apply log.
func matchLabel(rep *chapteraudio.Report) string {
	if !rep.HardTarget {
		return "audio-decided"
	}
	if rep.CountMatched {
		return "count matched"
	}
	return "count UNMATCHED"
}

func audioFileAnalysisDuration(f catalog.AudioFile) float64 {
	if f.DurationMs > 0 {
		return float64(f.DurationMs) / 1000
	}
	return float64(f.DurationSeconds)
}

// audioChapterSignature fingerprints the inputs an analysis ran on as
// "<analyzerVersion>:<fileHash>". The two parts are deliberately separable so
// the pass can tell "this book's FILES changed" (eligible on any pass) apart
// from "only the ALGORITHM changed" (eligible only when a full scan asks for a
// version migration) — folding both into one opaque hash is what used to make a
// version bump look like every book in the library had changed, and re-decode
// the world at the next trigger.
func audioChapterSignature(files []catalog.AudioFile) string {
	return audioChapterAnalyzerVersion + ":" + audioChapterFileHash(files)
}

// audioChapterFileHash fingerprints just the audio inputs: each file's checksum
// (or path|size when no checksum is stored).
func audioChapterFileHash(files []catalog.AudioFile) string {
	h := sha256.New()
	for _, f := range files {
		key := strings.TrimSpace(f.Checksum)
		if key == "" {
			key = fmt.Sprintf("%s|%d", f.Path, f.SizeBytes)
		}
		h.Write([]byte(key))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// chapterAnalysisEligible decides whether one book is in scope for this pass,
// from its stored signature and current files alone (no decode):
//
//   - never analyzed (empty sig) → eligible: it's a new book.
//   - files changed since the last analysis → eligible: the old result describes
//     audio that no longer exists.
//   - same files, older analyzer version (including pre-split opaque sigs, which
//     can't prove their file hash) → eligible ONLY for a migrate/force pass: the
//     stored chapters are still the best answer the previous algorithm produced,
//     and re-deriving them is heavy, so it waits for an explicit full scan.
//   - same files, same version, but a WEAK result (one-chapter-per-file, no
//     chapters, or an unverified audio guess) → eligible for a migrate/force pass.
//     A failed identification used to be cached identically to a success: once a
//     book fell back to file chapters under the current version, every later full
//     scan skipped it and it stayed broken forever. Retrying weak books on a full
//     scan lets improved metadata / a now-reachable Audnexus rescue them. (Quick
//     scans still skip — the decode is heavy and a full scan is the explicit ask.)
//   - same files, same version, authoritative result (embedded/cue markers, a
//     verified Audnexus edition, or a confident audio-aligned convergence) → skip
//     (force overrides).
func chapterAnalysisEligible(scope ChapterPassScope, storedSig, storedSource string, files []catalog.AudioFile) bool {
	// Real in-file markers (embedded chapter atoms or a cue sheet) are Audible's own
	// authored chapters — usually exact. The audio analysis cannot improve on them,
	// and decoding EVERY such book is what pegs the box on a full-library pass (and
	// risks replacing a perfect marker with a silence-snapped guess). So never run
	// the heavy pass on a book that already has authoritative markers — not even a
	// forced/full scan. Most libraries are mostly single-file AAX, so this keeps the
	// pass surgical: only the handful of marker-less rips get decoded. (The single-
	// book `chapters-inspect <id>` path bypasses eligibility, for debugging.)
	switch strings.TrimSpace(storedSource) {
	case chapterSourceEmbedded, chapterSourceCue:
		return false
	}
	if scope == ChapterPassForce {
		return true
	}
	storedSig = strings.TrimSpace(storedSig)
	if storedSig == "" {
		return true
	}
	ver, fileHash, ok := strings.Cut(storedSig, ":")
	if !ok {
		// Pre-split signature: version unknown-but-old, file hash unrecoverable.
		return scope == ChapterPassMigrate
	}
	if fileHash != audioChapterFileHash(files) {
		return true
	}
	if ver != audioChapterAnalyzerVersion {
		return scope == ChapterPassMigrate
	}
	// Up to date on this version and these files. A full scan still re-attempts a
	// book whose stored chapters are weak, so a prior failed identification is not
	// a life sentence.
	if scope == ChapterPassMigrate && chapterSourceIsWeak(storedSource) {
		return true
	}
	return false
}
