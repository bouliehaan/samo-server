package scannerstore

import (
	"context"
	"fmt"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
)

// UpsertAudiobook writes the audiobook row and returns the id it now lives
// under — which is not always the id passed in.
//
// A UNIQUE violation on path means a row already occupies this folder under a
// different id, left over from a scan when the library_id hashed differently.
// Adopting that row's id keeps its media files, chapters, progress and manual
// metadata attached; inserting a second row would strand all of it.
//
// progress_json is written on insert but deliberately absent from the update
// branch: listening position belongs to the listener, and a rescan must never
// reset it.
func (s *Store) UpsertAudiobook(ctx context.Context, item catalog.AudiobookItem) (string, error) {
	coverJSON := "{}"
	if item.Cover != nil {
		coverJSON = jsonText(item.Cover)
	}
	var bookJSON any
	if item.Book != nil {
		bookJSON = jsonText(item.Book)
	}

	_, err := s.exec(ctx, `
		INSERT INTO audiobooks (
		  id, library_id, path, folder_id, inode, size_bytes, missing, invalid,
		  cover_json, tags_json, genres_json, duration_seconds, progress_json, book_json,
		  updated_at, last_scan_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  library_id = excluded.library_id,
		  path = excluded.path,
		  folder_id = excluded.folder_id,
		  inode = excluded.inode,
		  size_bytes = excluded.size_bytes,
		  missing = excluded.missing,
		  invalid = excluded.invalid,
		  cover_json = excluded.cover_json,
		  tags_json = excluded.tags_json,
		  genres_json = excluded.genres_json,
		  duration_seconds = excluded.duration_seconds,
		  book_json = excluded.book_json,
		  updated_at = CURRENT_TIMESTAMP,
		  last_scan_at = CURRENT_TIMESTAMP`,
		item.ID, item.LibraryID, item.Path, item.FolderID, item.Inode, item.SizeBytes,
		boolInt(item.Missing), boolInt(item.Invalid), coverJSON, jsonText(item.Tags), jsonText(item.Genres),
		item.DurationSeconds, jsonText(item.Progress), bookJSON)
	if err == nil {
		return item.ID, nil
	}
	if !storage.IsUniqueViolation(err) {
		return "", fmt.Errorf("upsert audiobook %q: %w", item.ID, err)
	}

	var existingID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM audiobooks WHERE path = ?`, item.Path).Scan(&existingID); err != nil {
		return "", fmt.Errorf("resolve audiobook id by path %q: %w", item.Path, err)
	}
	if _, err := s.exec(ctx, `
		UPDATE audiobooks
		SET library_id = ?, folder_id = ?, inode = ?, size_bytes = ?, missing = ?, invalid = ?,
		    cover_json = ?, tags_json = ?, genres_json = ?, duration_seconds = ?, book_json = ?,
		    updated_at = CURRENT_TIMESTAMP, last_scan_at = CURRENT_TIMESTAMP
		WHERE path = ?`,
		item.LibraryID, item.FolderID, item.Inode, item.SizeBytes,
		boolInt(item.Missing), boolInt(item.Invalid), coverJSON, jsonText(item.Tags), jsonText(item.Genres),
		item.DurationSeconds, bookJSON, item.Path); err != nil {
		return "", fmt.Errorf("update audiobook by path %q: %w", item.Path, err)
	}
	return existingID, nil
}

// UpsertContributor writes an author or narrator row. Idempotent, and called
// for every contributor before the junction row that references it — which is
// what closes the foreign-key hole that used to appear when a narrator was
// linked without ever having been written.
func (s *Store) UpsertContributor(ctx context.Context, contributor catalog.Contributor) error {
	_, err := s.exec(ctx, `
		INSERT INTO contributors (id, name, sort_name, description, images_json, external_ids_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  sort_name = excluded.sort_name,
		  description = excluded.description,
		  images_json = excluded.images_json,
		  external_ids_json = excluded.external_ids_json`,
		contributor.ID, contributor.Name, contributor.SortName, contributor.Description,
		jsonText(contributor.Images), jsonText(contributor.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert contributor %q: %w", contributor.Name, err)
	}
	return nil
}

// ClearAudiobookContributors drops the book's author/narrator links.
func (s *Store) ClearAudiobookContributors(ctx context.Context, audiobookID string) error {
	if _, err := s.exec(ctx, `DELETE FROM audiobook_contributors WHERE audiobook_id = ?`, audiobookID); err != nil {
		return fmt.Errorf("clear audiobook contributors: %w", err)
	}
	return nil
}

// LinkAudiobookContributor attaches one contributor to the book in the given
// role and position.
func (s *Store) LinkAudiobookContributor(ctx context.Context, audiobookID, contributorID, role string, position int) error {
	if _, err := s.exec(ctx, `
		INSERT INTO audiobook_contributors (audiobook_id, contributor_id, role, position)
		VALUES (?, ?, ?, ?)`,
		audiobookID, contributorID, role, position); err != nil {
		return fmt.Errorf("insert audiobook contributor: %w", err)
	}
	return nil
}

// UpsertSeries writes the series row.
func (s *Store) UpsertSeries(ctx context.Context, series catalog.Series) error {
	_, err := s.exec(ctx, `
		INSERT INTO series (id, name, description, authors_json, item_ids_json, external_ids_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  description = excluded.description,
		  authors_json = excluded.authors_json,
		  item_ids_json = excluded.item_ids_json,
		  external_ids_json = excluded.external_ids_json`,
		series.ID, series.Name, series.Description, jsonText(series.Authors),
		jsonText(series.AudiobookIDs), jsonText(series.ExternalIDs))
	if err != nil {
		return fmt.Errorf("upsert series %q: %w", series.Name, err)
	}
	return nil
}

// ReplaceAudiobookSeries rewrites the book's series memberships.
func (s *Store) ReplaceAudiobookSeries(ctx context.Context, audiobookID string, series []catalog.SeriesRef) error {
	if _, err := s.exec(ctx, `DELETE FROM audiobook_series WHERE audiobook_id = ?`, audiobookID); err != nil {
		return fmt.Errorf("clear audiobook series: %w", err)
	}
	for _, entry := range series {
		if entry.ID == "" {
			continue
		}
		if _, err := s.exec(ctx, `
			INSERT INTO audiobook_series (audiobook_id, series_id, sequence, sequence_text)
			VALUES (?, ?, ?, ?)`,
			audiobookID, entry.ID, entry.Sequence, entry.SequenceText); err != nil {
			return fmt.Errorf("insert audiobook series: %w", err)
		}
	}
	return nil
}

// ReplaceAudiobookChapters rewrites the book's chapter list.
//
// Chapter ids are assigned by the caller: they are derived from the book, the
// index, the title and the start offset, so a re-scan that produces the same
// chapters produces the same ids.
func (s *Store) ReplaceAudiobookChapters(ctx context.Context, audiobookID string, chapters []catalog.AudioChapter) error {
	if _, err := s.exec(ctx, `DELETE FROM audiobook_chapters WHERE audiobook_id = ?`, audiobookID); err != nil {
		return fmt.Errorf("clear audiobook chapters: %w", err)
	}
	for _, chapter := range chapters {
		// start_seconds/end_seconds stay for back-compat reads; start_ms/end_ms
		// are the precise canonical values the API now projects.
		if _, err := s.exec(ctx, `
			INSERT INTO audiobook_chapters (id, audiobook_id, chapter_index, title, start_seconds, end_seconds, start_ms, end_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			chapter.ID, audiobookID, chapter.Index, chapter.Title,
			int(chapter.StartSeconds), int(chapter.EndSeconds), chapter.StartMs(), chapter.EndMs()); err != nil {
			return fmt.Errorf("insert audiobook chapter %q: %w", chapter.Title, err)
		}
	}
	return nil
}

// SetAudiobookChapterProvenance records where a book's chapters came from
// (embedded/cue/file/none, or "audnexus"), the ASIN they were resolved from
// when external, and when that sync happened. This is what makes a degraded
// book queryable instead of invisible. asin/syncedAt are empty/nil for
// file-derived chapters.
func (s *Store) SetAudiobookChapterProvenance(ctx context.Context, audiobookID, source, asin string, syncedAt *time.Time) error {
	if _, err := s.exec(ctx, `
		UPDATE audiobooks
		SET chapter_source = ?, chapter_asin = ?, chapter_synced_at = ?
		WHERE id = ?`,
		source, asin, timeString(syncedAt), audiobookID); err != nil {
		return fmt.Errorf("set audiobook chapter provenance for %q: %w", audiobookID, err)
	}
	return nil
}

// SetAudioChapterMetrics records the audio chapter analysis confidence (0..1)
// and the input signature it ran on. The signature lets a later scan skip a
// book whose files and analyzer version are unchanged, so the expensive
// full-file decode happens once per file version rather than every scan.
func (s *Store) SetAudioChapterMetrics(ctx context.Context, audiobookID string, confidence float64, sig string) error {
	if _, err := s.exec(ctx, `
		UPDATE audiobooks
		SET chapter_confidence = ?, chapter_audio_sig = ?
		WHERE id = ?`,
		confidence, sig, audiobookID); err != nil {
		return fmt.Errorf("set audio chapter metrics for %q: %w", audiobookID, err)
	}
	return nil
}
