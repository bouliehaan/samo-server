package scanner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/chapteraudio"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

// alignTestDB opens a migrated scratch DB with one audiobook and its media_files
// rows, returning the scanner and book id.
func alignTestDB(t *testing.T, ffmpeg, bookPath string, files []catalog.AudioFile, chapterSource string, chapters []catalog.AudioChapter) (*Scanner, *sql.DB, string) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	s := NewWithOptions(db, Options{FFmpegPath: ffmpeg})
	lib := Library{ID: "lib-books", Name: "Books", Kind: "audiobook", Path: filepath.Dir(bookPath)}
	if err := s.upsertLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	bookID := stableID("audiobook", lib.ID, bookPath)
	if _, err := s.upsertAudiobook(ctx, catalog.AudiobookItem{
		ID:        bookID,
		LibraryID: lib.ID,
		Path:      bookPath,
		Book:      &catalog.BookMetadata{Title: "Test Book"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		f.ID = stableID("file", f.Path)
		if err := s.upsertAudioFile(ctx, lib.ID, audioFileOwner{AudiobookID: bookID}, f, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.replaceAudiobookChapters(ctx, bookID, chapters); err != nil {
		t.Fatal(err)
	}
	if chapterSource != "" {
		if err := s.setAudiobookChapterProvenance(ctx, bookID, chapterSource, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	return s, db, bookID
}

func chapterRows(t *testing.T, db *sql.DB, bookID string) (int, string) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM audiobook_chapters WHERE audiobook_id = ?`, bookID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	var source string
	if err := db.QueryRowContext(context.Background(),
		`SELECT COALESCE(chapter_source,'') FROM audiobooks WHERE id = ?`, bookID).Scan(&source); err != nil {
		t.Fatal(err)
	}
	return count, source
}

func junkChapters(n int) []catalog.AudioChapter {
	out := make([]catalog.AudioChapter, n)
	for i := range out {
		out[i] = catalog.AudioChapter{Index: i + 1, Title: "Junk", StartSeconds: float64(i) * 10, EndSeconds: float64(i+1) * 10}
	}
	return out
}

// An audio proposal WITHOUT an Audnexus anchor must never be written, no matter
// how confident the detector was — unanchored writes are how the old algorithm
// replaced good chapters with guesses.
func TestApplyRefusesUnanchoredReport(t *testing.T) {
	files := []catalog.AudioFile{{Path: "/books/b/part1.mp3", DurationMs: 600_000, DurationSeconds: 600}}
	s, db, bookID := alignTestDB(t, "ffmpeg", "/books/b", files, chapterSourceEmbedded,
		[]catalog.AudioChapter{{Index: 1, Title: "Real Embedded", StartSeconds: 0, EndSeconds: 600}})

	rep := &chapteraudio.Report{
		Recommendation: chapteraudio.RecommendApply, // confident — but unanchored
		HardTarget:     false,
		Confidence:     0.99,
		DurationSec:    600,
		Chapters: []chapteraudio.ProposedChapter{
			{Index: 1, Title: "Guess 1", StartSec: 0, EndSec: 300},
			{Index: 2, Title: "Guess 2", StartSec: 300, EndSec: 600},
		},
	}
	wrote, err := s.ApplyAudioChapterReport(context.Background(), bookID, rep, files, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Fatalf("unanchored report must not be written")
	}
	count, source := chapterRows(t, db, bookID)
	if count != 1 || source != chapterSourceEmbedded {
		t.Fatalf("embedded chapters disturbed: count=%d source=%q", count, source)
	}
}

// THE fix for "chapters are all wrong": when the audio cannot confidently
// converge to the verified count, the book must NOT fall back to one-chapter-per-
// file. A resolved Audnexus edition's named markers are written instead — they
// beat track splits even though the audio could not refine them.
func TestApplyWritesAudnexusEditionWhenAudioCannotConverge(t *testing.T) {
	root := t.TempDir()
	files := []catalog.AudioFile{
		{Path: filepath.Join(root, "cd1.mp3"), RelativePath: "cd1.mp3", DurationMs: 600_000, DurationSeconds: 600},
		{Path: filepath.Join(root, "cd2.mp3"), RelativePath: "cd2.mp3", DurationMs: 600_000, DurationSeconds: 600},
	}
	// Book is currently stuck on degenerate one-chapter-per-file ("file") chapters.
	s, db, bookID := alignTestDB(t, "ffmpeg", root, files, chapterSourceFile, junkChapters(2))

	// The audio analysis ran with a hard target but could not match the count.
	rep := &chapteraudio.Report{Recommendation: chapteraudio.RecommendReview, HardTarget: true, Confidence: 0.3, AudioCount: 5, TargetCount: 3, DurationSec: 1200}
	anchor := []catalog.AudioChapter{
		{Index: 1, Title: "Opening Credits", StartSeconds: 0, EndSeconds: 30},
		{Index: 2, Title: "A Twin Disaster", StartSeconds: 30, EndSeconds: 700},
		{Index: 3, Title: "The Council of Elders", StartSeconds: 700, EndSeconds: 1180},
	}
	dbFiles, err := catalog.AudiobookAudioFiles(context.Background(), db, bookID)
	if err != nil {
		t.Fatal(err)
	}
	wrote, err := s.ApplyAudioChapterReport(context.Background(), bookID, rep, dbFiles, "B002UZKL7A", anchor)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatalf("a verified Audnexus edition must be written over weak file chapters")
	}
	count, source := chapterRows(t, db, bookID)
	if count != 3 || source != ChapterSourceAudnexus {
		t.Fatalf("expected 3 Audnexus chapters (source %q), got count=%d source=%q", ChapterSourceAudnexus, count, source)
	}
	// The last chapter end is anchored to the real runtime.
	var lastEnd float64
	if err := db.QueryRowContext(context.Background(),
		`SELECT end_seconds FROM audiobook_chapters WHERE audiobook_id = ? ORDER BY start_seconds DESC LIMIT 1`, bookID).Scan(&lastEnd); err != nil {
		t.Fatal(err)
	}
	if lastEnd != 1200 {
		t.Fatalf("last chapter end = %v, want 1200 (anchored to file runtime)", lastEnd)
	}
}

// The Audnexus fallback must never DOWNGRADE authoritative in-file markers: a
// book on real embedded chapters keeps them even when an edition resolves and the
// audio declined to converge (embedded positions are real and cannot drift).
func TestApplyAudnexusFallbackNeverOverwritesEmbedded(t *testing.T) {
	files := []catalog.AudioFile{{Path: "/books/b/book.m4b", DurationMs: 1_200_000, DurationSeconds: 1200}}
	s, db, bookID := alignTestDB(t, "ffmpeg", "/books/b", files, chapterSourceEmbedded,
		[]catalog.AudioChapter{{Index: 1, Title: "Real Embedded", StartSeconds: 0, EndSeconds: 1200}})

	rep := &chapteraudio.Report{Recommendation: chapteraudio.RecommendReview, HardTarget: true, Confidence: 0.3, DurationSec: 1200}
	anchor := []catalog.AudioChapter{
		{Index: 1, Title: "Audnexus 1", StartSeconds: 0, EndSeconds: 600},
		{Index: 2, Title: "Audnexus 2", StartSeconds: 600, EndSeconds: 1200},
	}
	wrote, err := s.ApplyAudioChapterReport(context.Background(), bookID, rep, files, "B000", anchor)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Fatalf("must not overwrite authoritative embedded chapters with the Audnexus fallback")
	}
	count, source := chapterRows(t, db, bookID)
	if count != 1 || source != chapterSourceEmbedded {
		t.Fatalf("embedded chapters disturbed: count=%d source=%q", count, source)
	}
}

// Books still carrying v1/v2 audio-guess chapters get healed back to file truth
// when the v3 pass declines to write: junk rows are replaced by the honest
// one-chapter-per-file layout with honest provenance.
func TestApplyHealsStaleAudioGuessChapters(t *testing.T) {
	root := t.TempDir()
	files := []catalog.AudioFile{
		{Path: filepath.Join(root, "cd1.mp3"), RelativePath: "cd1.mp3", DurationMs: 600_000, DurationSeconds: 600},
		{Path: filepath.Join(root, "cd2.mp3"), RelativePath: "cd2.mp3", DurationMs: 540_000, DurationSeconds: 540},
	}
	s, db, bookID := alignTestDB(t, "ffmpeg", root, files, chapterSourceAudioDetected, junkChapters(150))

	rep := &chapteraudio.Report{Recommendation: chapteraudio.RecommendReview, HardTarget: true, Confidence: 0.3}
	dbFiles, err := catalog.AudiobookAudioFiles(context.Background(), db, bookID)
	if err != nil {
		t.Fatal(err)
	}
	wrote, err := s.ApplyAudioChapterReport(context.Background(), bookID, rep, dbFiles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Fatalf("review report must not be written")
	}
	count, source := chapterRows(t, db, bookID)
	if count != 2 || source != chapterSourceFile {
		t.Fatalf("expected heal to 2 one-per-file chapters with source %q, got count=%d source=%q",
			chapterSourceFile, count, source)
	}

	// Second decline is a no-op: provenance is no longer audio-derived.
	if _, err := s.ApplyAudioChapterReport(context.Background(), bookID, rep, dbFiles, "", nil); err != nil {
		t.Fatal(err)
	}
	count2, source2 := chapterRows(t, db, bookID)
	if count2 != 2 || source2 != chapterSourceFile {
		t.Fatalf("heal must be idempotent, got count=%d source=%q", count2, source2)
	}
}

// The pass reuses the ASIN a previous scan verified (chapter_asin) when the
// rip's tags carry none, so scan and pass can never disagree about the edition.
func TestLookupReusesVerifiedChapterASIN(t *testing.T) {
	files := []catalog.AudioFile{{Path: "/books/b/book.m4b", DurationMs: 3_600_000, DurationSeconds: 3600}}
	s, db, bookID := alignTestDB(t, "ffmpeg", "/books/b", files, "", nil)
	if _, err := db.ExecContext(context.Background(),
		`UPDATE audiobooks SET chapter_asin = 'B002UZKL7A' WHERE id = ?`, bookID); err != nil {
		t.Fatal(err)
	}

	lookup := s.audiobookChapterLookup(context.Background(), bookID, files)
	if lookup.ASIN != "B002UZKL7A" {
		t.Fatalf("lookup.ASIN = %q, want the verified chapter_asin", lookup.ASIN)
	}
	if lookup.DurationSeconds != 3600 {
		t.Fatalf("lookup.DurationSeconds = %v, want 3600", lookup.DurationSeconds)
	}
}

// THE library-wide identification bug: the clean title + matched ASIN live in a
// metadata override (what the catalog/API serves), while raw book_json holds only
// the folder name and no ASIN. The chapter lookup must apply the override, or it
// searches Audible for "Eragon - Inheritance Book 01" and finds nothing — leaving
// the whole library on file chapters despite Samo already knowing every ASIN.
func TestChapterLookupAppliesMetadataOverride(t *testing.T) {
	files := []catalog.AudioFile{{Path: "/books/eragon/01.mp3", DurationMs: 600_000, DurationSeconds: 600}}
	s, db, bookID := alignTestDB(t, "ffmpeg", "/books/eragon", files, "", nil)
	if _, err := db.ExecContext(context.Background(),
		`UPDATE audiobooks SET book_json = ? WHERE id = ?`,
		`{"title":"Eragon - Inheritance Book 01"}`, bookID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO metadata_overrides (target_kind, target_id, fields_json) VALUES ('audiobook', ?, ?)`,
		bookID,
		`{"title":"Eragon","authors":[{"name":"Christopher Paolini"}],"externalIds":{"asin":"B002UZKL7A","audibleAsin":"B002UZKL7A"}}`,
	); err != nil {
		t.Fatal(err)
	}

	lookup := s.audiobookChapterLookup(context.Background(), bookID, files)
	if lookup.ASIN != "B002UZKL7A" {
		t.Fatalf("lookup.ASIN = %q, want B002UZKL7A from the override (raw book_json has none)", lookup.ASIN)
	}
	if lookup.Title != "Eragon" {
		t.Fatalf("lookup.Title = %q, want the override title \"Eragon\"", lookup.Title)
	}
	if lookup.Author != "Christopher Paolini" {
		t.Fatalf("lookup.Author = %q, want Christopher Paolini from the override", lookup.Author)
	}
}

// THE feedback-loop regression: with no provider (or a provider miss), stored
// chapters must NOT become the convergence target — the analyzer runs blind
// (diagnostic), so yesterday's wrong count can never launder itself into
// today's "match". Runs the real decode path through ffmpeg.
func TestAnalyzeWithoutAnchorIgnoresStoredChapters(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	root := t.TempDir()
	wav := filepath.Join(root, "book.wav")
	writeTestWAV(t, wav, 30) // 30s of tone, no real chapter structure

	files := []catalog.AudioFile{{Path: wav, RelativePath: "book.wav", DurationMs: 30_000, DurationSeconds: 30}}
	s, _, bookID := alignTestDB(t, ffmpeg, root, files, chapterSourceAudioDetected, junkChapters(150))

	rep, _, asin, anchor, err := s.AnalyzeAudiobookChapters(context.Background(), bookID)
	if err != nil {
		t.Fatal(err)
	}
	if asin != "" {
		t.Fatalf("no provider configured, asin should be empty, got %q", asin)
	}
	if len(anchor) != 0 {
		t.Fatalf("no provider configured, anchor should be empty, got %d", len(anchor))
	}
	if rep.HardTarget {
		t.Fatalf("no provider configured, report must not claim a hard target")
	}
	if rep.MetadataCount != 0 {
		t.Fatalf("stored chapters leaked into the analysis as metadata: MetadataCount=%d, want 0", rep.MetadataCount)
	}
}

// writeTestWAV writes a mono 16-bit PCM WAV of `seconds` of quiet tone at the
// chapteraudio analysis rate.
func writeTestWAV(t *testing.T, path string, seconds int) {
	t.Helper()
	const rate = chapteraudio.SampleRate
	n := seconds * rate
	var buf bytes.Buffer
	le := binary.LittleEndian
	dataLen := n * 2
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, le, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, le, uint32(16))
	_ = binary.Write(&buf, le, uint16(1))
	_ = binary.Write(&buf, le, uint16(1))
	_ = binary.Write(&buf, le, uint32(rate))
	_ = binary.Write(&buf, le, uint32(rate*2))
	_ = binary.Write(&buf, le, uint16(2))
	_ = binary.Write(&buf, le, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(&buf, le, uint32(dataLen))
	for i := 0; i < n; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*300*float64(i)/rate))
		_ = binary.Write(&buf, le, v)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
