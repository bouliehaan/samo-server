package scanner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
)

// runPhaseCrossLibraryMoves searches other libraries for tracks still marked
// missing after within-library reconciliation (Navidrome phase 2 stage 2).
func (s *Scanner) runPhaseCrossLibraryMoves(ctx context.Context, libraries []Library) error {
	if len(libraries) <= 1 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, library_id, path, track_id, track_pid, content_hash
		FROM media_files
		WHERE missing = 1 AND track_id IS NOT NULL AND TRIM(track_pid) != ''`)
	if err != nil {
		return fmt.Errorf("list cross-library missing tracks: %w", err)
	}
	defer rows.Close()

	matched := 0
	state := &scanState{}
	for rows.Next() {
		var missing indexedMediaFile
		if err := rows.Scan(&missing.ID, &missing.LibraryID, &missing.Path, &missing.TrackID, &missing.TrackPID, &missing.ContentHash); err != nil {
			return err
		}
		missing.Missing = true
		found, err := s.findCrossLibraryMatch(ctx, missing)
		if err != nil {
			log.Printf("scanner: cross-library match for %q: %v", missing.Path, err)
			continue
		}
		if found.ID == "" {
			continue
		}
		if err := s.moveMatchedTrack(ctx, missing.LibraryID, found, missing); err != nil {
			log.Printf("scanner: cross-library move %q: %v", missing.Path, err)
			continue
		}
		matched++
		state.noteChange()
	}
	if matched > 0 {
		log.Printf("scanner: cross-library reconciled %d moved track(s)", matched)
	}
	return rows.Err()
}

func (s *Scanner) findCrossLibraryMatch(ctx context.Context, missing indexedMediaFile) (indexedMediaFile, error) {
	if mb := musicBrainzTrackIDFromRow(ctx, s.db, missing.ID); mb != "" {
		match, ok, err := s.findRecentByMBZTrackID(ctx, missing, mb)
		if err != nil {
			return indexedMediaFile{}, err
		}
		if ok {
			return match, nil
		}
	}
	if missing.ContentHash != "" {
		match, ok, err := s.findRecentByContentHash(ctx, missing)
		if err != nil {
			return indexedMediaFile{}, err
		}
		if ok {
			return match, nil
		}
	}
	return s.findRecentByFileName(ctx, missing)
}

func musicBrainzTrackIDFromRow(ctx context.Context, db *sql.DB, fileID string) string {
	var tagsJSON string
	if err := db.QueryRowContext(ctx, `SELECT embedded_tags_json FROM media_files WHERE id = ?`, fileID).Scan(&tagsJSON); err != nil {
		return ""
	}
	var flat map[string]string
	if err := json.Unmarshal([]byte(tagsJSON), &flat); err != nil {
		return ""
	}
	tags := normalizeTags(flat)
	return firstTag(tags, "musicbrainz_trackid", "musicbrainz_recordingid")
}

func (s *Scanner) findRecentByMBZTrackID(ctx context.Context, missing indexedMediaFile, mbzID string) (indexedMediaFile, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, library_id, path, track_id, track_pid, content_hash
		FROM media_files
		WHERE missing = 0 AND library_id != ? AND (
			json_extract(embedded_tags_json, '$.musicbrainz_trackid') = ? OR
			json_extract(embedded_tags_json, '$.musicbrainz_recordingid') = ?
		)
		ORDER BY updated_at DESC LIMIT 8`,
		missing.LibraryID, mbzID, mbzID)
	if err != nil {
		return indexedMediaFile{}, false, err
	}
	defer rows.Close()
	return firstMatchingRow(rows, missing)
}

func (s *Scanner) findRecentByContentHash(ctx context.Context, missing indexedMediaFile) (indexedMediaFile, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, library_id, path, track_id, track_pid, content_hash
		FROM media_files
		WHERE missing = 0 AND library_id != ? AND content_hash = ?
		ORDER BY updated_at DESC LIMIT 8`,
		missing.LibraryID, missing.ContentHash)
	if err != nil {
		return indexedMediaFile{}, false, err
	}
	defer rows.Close()
	return firstMatchingRow(rows, missing)
}

func (s *Scanner) findRecentByFileName(ctx context.Context, missing indexedMediaFile) (indexedMediaFile, error) {
	base := filepath.Base(missing.Path)
	if base == "" {
		return indexedMediaFile{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, library_id, path, track_id, track_pid, content_hash
		FROM media_files
		WHERE missing = 0 AND library_id != ? AND file_name = ?
		ORDER BY updated_at DESC LIMIT 8`,
		missing.LibraryID, base)
	if err != nil {
		return indexedMediaFile{}, err
	}
	defer rows.Close()
	match, ok, err := firstMatchingRow(rows, missing)
	if err != nil || !ok {
		return indexedMediaFile{}, err
	}
	return match, nil
}

func firstMatchingRow(rows *sql.Rows, missing indexedMediaFile) (indexedMediaFile, bool, error) {
	var candidates []indexedMediaFile
	for rows.Next() {
		var row indexedMediaFile
		if err := rows.Scan(&row.ID, &row.LibraryID, &row.Path, &row.TrackID, &row.TrackPID, &row.ContentHash); err != nil {
			return indexedMediaFile{}, false, err
		}
		candidates = append(candidates, row)
	}
	if err := rows.Err(); err != nil {
		return indexedMediaFile{}, false, err
	}
	for _, c := range candidates {
		if mediaFileEquals(missing, c) {
			return c, true, nil
		}
	}
	if len(candidates) == 1 && mediaFileEquivalent(missing, candidates[0]) {
		return candidates[0], true, nil
	}
	return indexedMediaFile{}, false, nil
}
