package scannerstore

import (
	"context"
	"fmt"
)

// AnalysisBook is what the audio chapter pass needs to decide whether a book is
// worth decoding: its path, the signature of the input the last analysis ran
// on, and the provenance of the chapters it currently has.
type AnalysisBook struct {
	ID     string
	Path   string
	Sig    string
	Source string
}

// AudiobookChapterSource returns a book's chapter provenance label, or "" when
// it has none.
func (s *Store) AudiobookChapterSource(ctx context.Context, audiobookID string) (string, error) {
	var source string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(chapter_source,'') FROM audiobooks WHERE id = ?`, audiobookID).Scan(&source)
	return source, err
}

// AudiobookPath returns a book's folder path, or "" when it has none.
func (s *Store) AudiobookPath(ctx context.Context, audiobookID string) (string, error) {
	var path string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(path,'') FROM audiobooks WHERE id = ?`, audiobookID).Scan(&path)
	return path, err
}

// AudiobookBookJSON returns a book's raw metadata blob and the ASIN its
// chapters were resolved from, defaulting to an empty object and "".
func (s *Store) AudiobookBookJSON(ctx context.Context, audiobookID string) (bookJSON, chapterASIN string, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(book_json,'{}'), COALESCE(chapter_asin,'') FROM audiobooks WHERE id = ?`,
		audiobookID).Scan(&bookJSON, &chapterASIN)
	return bookJSON, chapterASIN, err
}

// AudiobookPathAndBookJSON returns a book's path and metadata blob together.
func (s *Store) AudiobookPathAndBookJSON(ctx context.Context, audiobookID string) (path, bookJSON string, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(path,''), COALESCE(book_json,'{}') FROM audiobooks WHERE id = ?`,
		audiobookID).Scan(&path, &bookJSON)
	return path, bookJSON, err
}

// AudiobooksForAnalysis lists every book the chapter analyzer may consider.
func (s *Store) AudiobooksForAnalysis(ctx context.Context) ([]AnalysisBook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, COALESCE(path,''), COALESCE(chapter_audio_sig,''), COALESCE(chapter_source,'') FROM audiobooks`)
	if err != nil {
		return nil, fmt.Errorf("list audiobooks for analysis: %w", err)
	}
	defer rows.Close()

	var out []AnalysisBook
	for rows.Next() {
		var b AnalysisBook
		if err := rows.Scan(&b.ID, &b.Path, &b.Sig, &b.Source); err != nil {
			return nil, fmt.Errorf("scan audiobook row: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
