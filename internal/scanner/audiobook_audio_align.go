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
const audioChapterAnalyzerVersion = "v1"

// AudioChapterAnalysisEnabled reports whether the scanner can run audio-anchored
// chapter analysis — i.e. an ffmpeg binary was configured to decode audio.
func (s *Scanner) AudioChapterAnalysisEnabled() bool {
	return strings.TrimSpace(s.ffmpegPath) != ""
}

// AnalyzeAudiobookChapters decodes one book and proposes a chapter list derived
// from its own audio (silences), labelled with names borrowed from whatever
// chapters are already stored. It writes NOTHING — the inspector prints the
// report; ApplyAudioChapterReport persists it. Returns the report plus the files
// it analyzed so callers can compute the cache signature.
func (s *Scanner) AnalyzeAudiobookChapters(ctx context.Context, audiobookID string) (*chapteraudio.Report, []catalog.AudioFile, error) {
	if !s.AudioChapterAnalysisEnabled() {
		return nil, nil, fmt.Errorf("audio chapter analysis unavailable: no ffmpeg configured")
	}
	files, err := catalog.AudiobookAudioFiles(ctx, s.db, audiobookID)
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("audiobook %s has no audio files on disk", audiobookID)
	}
	meta, err := catalog.AudiobookStoredChapters(ctx, s.db, audiobookID)
	if err != nil {
		return nil, files, err
	}

	inputs := make([]chapteraudio.FileInput, 0, len(files))
	for _, f := range files {
		inputs = append(inputs, chapteraudio.FileInput{
			Path:        f.Path,
			DurationSec: audioFileAnalysisDuration(f),
			StartOffset: f.StartOffsetSeconds,
		})
	}

	rep, err := chapteraudio.NewAnalyzer(s.ffmpegPath).AnalyzeBook(ctx, inputs, meta)
	if err != nil {
		return nil, files, err
	}
	return rep, files, nil
}

// ApplyAudioChapterReport persists the analyzer's proposal. When the analyzer is
// confident enough to recommend applying, the audio-derived chapters REPLACE the
// stored ones (audio is the timing source of truth) and provenance is recorded.
// Either way the confidence and cache signature are stamped so the book is not
// re-decoded until its files — or the analyzer version — change. Returns whether
// chapters were rewritten.
func (s *Scanner) ApplyAudioChapterReport(ctx context.Context, audiobookID string, rep *chapteraudio.Report, files []catalog.AudioFile) (bool, error) {
	sig := audioChapterSignature(files)
	if rep == nil || rep.Recommendation != chapteraudio.RecommendApply {
		conf := 0.0
		if rep != nil {
			conf = rep.Confidence
		}
		return false, s.setAudioChapterMetrics(ctx, audiobookID, conf, sig)
	}

	chapters := fixChapterEndTimes(rep.AsAudioChapters(), rep.DurationSec)
	source := chapterSourceAudioAligned
	if !reportHasNames(rep) {
		source = chapterSourceAudioDetected
	}
	if err := s.replaceAudiobookChapters(ctx, audiobookID, chapters); err != nil {
		return false, err
	}
	now := time.Now().UTC()
	if err := s.setAudiobookChapterProvenance(ctx, audiobookID, source, "", &now); err != nil {
		return false, err
	}
	if err := s.setAudioChapterMetrics(ctx, audiobookID, rep.Confidence, sig); err != nil {
		return false, err
	}
	return true, nil
}

// RunChapterAnalysisPass analyzes every audiobook whose audio chapter signature
// is stale (or all of them, when force) and applies the proposals the analyzer
// is confident in. This is the decode-heavy pass — minutes per long book — so it
// is meant to run in the background AFTER a scan, never inline per file. It skips
// unchanged books cheaply (signature compare, no decode) and honors ctx
// cancellation between books.
func (s *Scanner) RunChapterAnalysisPass(ctx context.Context, force bool) (analyzed, applied int, err error) {
	if !s.AudioChapterAnalysisEnabled() {
		return 0, 0, nil
	}
	books, err := s.audiobooksForAnalysis(ctx)
	if err != nil {
		return 0, 0, err
	}
	log.Printf("scanner: audio chapter analysis pass over %d book(s) (force=%v)", len(books), force)

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
		sig := audioChapterSignature(files)
		if !force && sig == b.sig {
			continue // unchanged since last analysis — skip the expensive decode
		}

		rep, _, aerr := s.AnalyzeAudiobookChapters(ctx, b.id)
		if aerr != nil {
			log.Printf("scanner: audio chapters: analyze %q failed: %v", b.label(), aerr)
			continue
		}
		analyzed++
		didApply, werr := s.ApplyAudioChapterReport(ctx, b.id, rep, files)
		if werr != nil {
			log.Printf("scanner: audio chapters: apply %q failed: %v", b.label(), werr)
			continue
		}
		if didApply {
			applied++
			log.Printf("scanner: audio chapters APPLIED %q — %d chapters, conf %.2f, split %.2fs, source %s",
				b.label(), rep.AudioCount, rep.Confidence, rep.SplitSeconds, reportSource(rep))
		} else {
			log.Printf("scanner: audio chapters KEPT existing for %q — %s (conf %.2f, audio=%d vs metadata=%d)",
				b.label(), rep.Recommendation, rep.Confidence, rep.AudioCount, rep.MetadataCount)
		}
	}
	log.Printf("scanner: audio chapter analysis pass done — analyzed %d, applied %d", analyzed, applied)
	return analyzed, applied, nil
}

type analysisBook struct {
	id   string
	path string
	sig  string
}

func (b analysisBook) label() string {
	if b.path != "" {
		return filepath.Base(b.path)
	}
	return b.id
}

func (s *Scanner) audiobooksForAnalysis(ctx context.Context) ([]analysisBook, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(path,''), COALESCE(chapter_audio_sig,'') FROM audiobooks`)
	if err != nil {
		return nil, fmt.Errorf("list audiobooks for analysis: %w", err)
	}
	defer rows.Close()
	var out []analysisBook
	for rows.Next() {
		var b analysisBook
		if err := rows.Scan(&b.id, &b.path, &b.sig); err != nil {
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

func audioFileAnalysisDuration(f catalog.AudioFile) float64 {
	if f.DurationMs > 0 {
		return float64(f.DurationMs) / 1000
	}
	return float64(f.DurationSeconds)
}

// audioChapterSignature fingerprints the inputs an analysis ran on: the analyzer
// version plus each file's checksum (or path|size when no checksum is stored).
// A book whose signature is unchanged is skipped without decoding.
func audioChapterSignature(files []catalog.AudioFile) string {
	h := sha256.New()
	h.Write([]byte(audioChapterAnalyzerVersion))
	h.Write([]byte{0})
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
