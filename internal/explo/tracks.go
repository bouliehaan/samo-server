package explo

import (
	"context"
	"fmt"
)

// LedgerRow is one explo_tracks entry as exposed to the API/UI: the raw
// pipeline state for a track, without display decoration (the API layer
// resolves current titles from the catalog projection, which is override-
// aware; matched_* here are what identification reported at match time).
type LedgerRow struct {
	TrackID       string  `json:"trackId"`
	Status        string  `json:"status"`
	Attempts      int     `json:"attempts"`
	Error         string  `json:"error,omitempty"`
	ProcessedAt   string  `json:"processedAt"`
	CoverStatus   string  `json:"coverStatus"`
	CoverAttempts int     `json:"coverAttempts"`
	MatchedTitle  string  `json:"matchedTitle,omitempty"`
	MatchedArtist string  `json:"matchedArtist,omitempty"`
	Score         float64 `json:"score,omitempty"`
	AlbumID       string  `json:"albumId,omitempty"`
}

// LedgerSummary aggregates the pipeline state for the Explo tab header.
type LedgerSummary struct {
	InFolder      int    `json:"inFolder"`
	Identified    int    `json:"identified"`
	AwaitingRetry int    `json:"awaitingRetry"`
	Retired       int    `json:"retired"`
	NextRetryAt   string `json:"nextRetryAt,omitempty"`
	CoversDone    int    `json:"coversDone"`
	CoversPending int    `json:"coversPending"`
	Placeholders  int    `json:"placeholders"`
}

// LedgerSnapshot is the summary plus per-track rows, scoped to the currently
// configured folder(s) — the same universe every other explo surface uses.
type LedgerSnapshot struct {
	Summary LedgerSummary `json:"summary"`
	Tracks  []LedgerRow   `json:"tracks"`
}

// Ledger returns the explo pipeline state for the UI: a summary and the
// per-track rows, newest first. limit <= 0 returns summary only.
func (s *Service) Ledger(ctx context.Context, limit int) (LedgerSnapshot, error) {
	if s == nil || s.db == nil {
		return LedgerSnapshot{}, ErrDisabled
	}
	snapshot := LedgerSnapshot{}
	dirs := s.effectiveDirs()
	clause, args := exploPathClause(dirs)

	summaryQuery := fmt.Sprintf(`
		SELECT
		  COUNT(*),
		  COALESCE(SUM(CASE WHEN et.status IN ('matched', 'matched-fallback') THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN et.status IN ('unmatched', 'error') AND et.attempts <  %[1]d THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN et.status IN ('unmatched', 'error') AND et.attempts >= %[1]d THEN 1 ELSE 0 END), 0),
		  COALESCE(MIN(CASE WHEN et.status IN ('unmatched', 'error') AND et.attempts < %[1]d THEN %[2]s END), ''),
		  COALESCE(SUM(CASE WHEN et.cover_status = '%[4]s' THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN et.cover_status IN ('', '%[5]s') THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN et.cover_status = '%[6]s' THEN 1 ELSE 0 END), 0)
		FROM music_tracks mt
		JOIN media_files mf ON mf.track_id = mt.id
		LEFT JOIN explo_tracks et ON et.track_id = mt.id
		WHERE %[3]s`,
		exploMaxIdentifyAttempts, exploNextDueTimeExpr("et.processed_at"), clause,
		coverStatusDone, coverStatusPending, coverStatusPlaceholder)
	if err := s.db.QueryRowContext(ctx, summaryQuery, args...).Scan(
		&snapshot.Summary.InFolder,
		&snapshot.Summary.Identified,
		&snapshot.Summary.AwaitingRetry,
		&snapshot.Summary.Retired,
		&snapshot.Summary.NextRetryAt,
		&snapshot.Summary.CoversDone,
		&snapshot.Summary.CoversPending,
		&snapshot.Summary.Placeholders,
	); err != nil {
		return LedgerSnapshot{}, fmt.Errorf("explo ledger summary: %w", err)
	}
	if limit <= 0 {
		return snapshot, nil
	}

	rowsQuery := fmt.Sprintf(`
		SELECT et.track_id, et.status, et.attempts, et.error, et.processed_at,
		       et.cover_status, et.cover_attempts, et.matched_title, et.matched_artist, et.score,
		       COALESCE(mt.album_id, '')
		FROM explo_tracks et
		JOIN music_tracks mt ON mt.id = et.track_id
		JOIN media_files mf ON mf.track_id = mt.id
		WHERE %s
		ORDER BY et.processed_at DESC, et.track_id
		LIMIT %d`, clause, limit)
	rows, err := s.db.QueryContext(ctx, rowsQuery, args...)
	if err != nil {
		return LedgerSnapshot{}, fmt.Errorf("explo ledger rows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row LedgerRow
		if err := rows.Scan(&row.TrackID, &row.Status, &row.Attempts, &row.Error, &row.ProcessedAt,
			&row.CoverStatus, &row.CoverAttempts, &row.MatchedTitle, &row.MatchedArtist, &row.Score,
			&row.AlbumID); err != nil {
			return LedgerSnapshot{}, err
		}
		snapshot.Tracks = append(snapshot.Tracks, row)
	}
	return snapshot, rows.Err()
}
