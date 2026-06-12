package scanner

import (
	"context"
	"log"
	"path/filepath"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// healAudioDerivedChapters restores a book's chapters to file-derived truth when
// they are still the output of the retired v1/v2 audio-guess algorithms. Those
// algorithms wrote chapters with provenance audio-aligned/audio-detected even
// without a verified Audnexus anchor; the v3 pass only writes anchored results,
// so any audio-* book it declines to (re)write is carrying chapters no current
// code would produce — a stale wrong answer that would otherwise persist until
// the files themselves changed. This rebuilds what a scan would have derived
// from the files (embedded markers, cue sidecar, or per-file layout) and stamps
// honest provenance. No-op for books whose chapters never came from the audio
// pass. Idempotent: after healing, provenance is no longer audio-*.
func (s *Scanner) healAudioDerivedChapters(ctx context.Context, audiobookID string, files []catalog.AudioFile) error {
	var source string
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(chapter_source,'') FROM audiobooks WHERE id = ?`, audiobookID,
	).Scan(&source); err != nil {
		return err
	}
	if source != chapterSourceAudioAligned && source != chapterSourceAudioDetected {
		return nil
	}
	if len(files) == 0 {
		return nil
	}

	chapters, newSource := s.fileTruthChapters(ctx, audiobookID, files)
	if len(chapters) == 0 {
		return nil
	}
	if err := s.replaceAudiobookChapters(ctx, audiobookID, chapters); err != nil {
		return err
	}
	if err := s.setAudiobookChapterProvenance(ctx, audiobookID, newSource, "", nil); err != nil {
		return err
	}
	log.Printf("scanner: audio chapters HEALED %q — replaced stale %s chapters with %d %s chapter(s) from the files",
		s.audiobookLabel(ctx, audiobookID), source, len(chapters), newSource)
	return nil
}

// fileTruthChapters re-derives the chapters the files themselves carry, without
// a full scan probe: embedded MP4 chapter atoms read straight from each file's
// header/tail windows, a cue sidecar, or the degenerate one-chapter-per-file
// layout. Offsets come from the stored per-file durations — the same book-global
// timeline the player uses.
func (s *Scanner) fileTruthChapters(ctx context.Context, audiobookID string, files []catalog.AudioFile) ([]catalog.AudioChapter, string) {
	probes := make([]probedFile, 0, len(files))
	hasEmbedded := false
	for _, f := range files {
		probe := probedFile{AudioFile: f}
		if chs, err := mp4ChaptersFromFile(f.Path); err == nil && len(chs) > 0 {
			probe.Chapters = chs
			hasEmbedded = true
		}
		probes = append(probes, probe)
	}

	chapters := flattenBookChapters(probes)
	if hasEmbedded {
		return chapters, chapterSourceEmbedded
	}

	var root string
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(path,'') FROM audiobooks WHERE id = ?`, audiobookID).Scan(&root)
	if root == "" && len(files) > 0 {
		root = filepath.Dir(files[0].Path)
	}
	if cue := readCueChapters(root, probes); len(cue) > 0 {
		return fixChapterEndTimes(cue, bookDurationSeconds(files)), chapterSourceCue
	}
	// No real markers anywhere: the honest shape is one chapter per file (a
	// single whole-book chapter for a one-file book).
	return chapters, chapterSourceFile
}

func bookDurationSeconds(files []catalog.AudioFile) float64 {
	var total float64
	for _, f := range files {
		total += audioFileAnalysisDuration(f)
	}
	return total
}
