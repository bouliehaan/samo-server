package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type RecordSessionInput struct {
	AudiobookID          string
	StartPositionSeconds int
	EndPositionSeconds   int
	Completed            bool
}

func (s *Service) RecordSession(ctx context.Context, userID string, input RecordSessionInput) (ListeningSession, error) {
	if s == nil || s.db == nil {
		return ListeningSession{}, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	audiobookID := strings.TrimSpace(input.AudiobookID)
	if userID == "" || audiobookID == "" {
		return ListeningSession{}, ErrInvalidInput
	}
	if err := assertAudiobookExists(ctx, s.db, audiobookID); err != nil {
		return ListeningSession{}, err
	}
	start := input.StartPositionSeconds
	end := input.EndPositionSeconds
	if start < 0 || end < 0 {
		return ListeningSession{}, ErrInvalidInput
	}
	duration := end - start
	if duration < 0 {
		duration = 0
	}
	now := time.Now().UTC()
	id := stableID("session", userID, audiobookID, now.Format(time.RFC3339Nano))
	nowText := now.Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO listening_sessions (
		  id, user_id, audiobook_id, started_at, ended_at,
		  start_position_seconds, end_position_seconds, duration_seconds, completed
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, audiobookID, nowText, nowText, start, end, duration, boolInt(input.Completed))
	if err != nil {
		return ListeningSession{}, fmt.Errorf("record listening session: %w", err)
	}
	return s.loadSession(ctx, userID, id)
}

func (s *Service) ListSessionsForAudiobook(ctx context.Context, userID, audiobookID string, limit int) ([]ListeningSession, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, audiobook_id, started_at, ended_at,
		       start_position_seconds, end_position_seconds, duration_seconds, completed
		FROM listening_sessions
		WHERE user_id = ? AND audiobook_id = ?
		ORDER BY started_at DESC
		LIMIT ?`, userID, audiobookID, limit)
	if err != nil {
		return nil, fmt.Errorf("list audiobook sessions: %w", err)
	}
	defer rows.Close()
	return scanSessions(rows)
}

func (s *Service) ListRecentSessions(ctx context.Context, userID string, limit int) ([]ListeningSession, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, audiobook_id, started_at, ended_at,
		       start_position_seconds, end_position_seconds, duration_seconds, completed
		FROM listening_sessions
		WHERE user_id = ?
		ORDER BY started_at DESC
		LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent sessions: %w", err)
	}
	defer rows.Close()
	return scanSessions(rows)
}

func (s *Service) loadSession(ctx context.Context, userID, id string) (ListeningSession, error) {
	var item ListeningSession
	var completed int
	var startedAt, endedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, audiobook_id, started_at, ended_at,
		       start_position_seconds, end_position_seconds, duration_seconds, completed
		FROM listening_sessions
		WHERE id = ?`, id).
		Scan(&item.ID, &item.UserID, &item.AudiobookID, &startedAt, &endedAt,
			&item.StartPositionSeconds, &item.EndPositionSeconds, &item.DurationSeconds, &completed)
	if err == sql.ErrNoRows {
		return ListeningSession{}, ErrNotFound
	}
	if err != nil {
		return ListeningSession{}, fmt.Errorf("load session: %w", err)
	}
	if strings.TrimSpace(userID) != "" && item.UserID != userID {
		return ListeningSession{}, ErrForbidden
	}
	item.Completed = completed != 0
	item.StartedAt = parseTimePtr(startedAt)
	item.EndedAt = parseTimePtr(endedAt)
	return item, nil
}

func scanSessions(rows *sql.Rows) ([]ListeningSession, error) {
	var items []ListeningSession
	for rows.Next() {
		var item ListeningSession
		var completed int
		var startedAt, endedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.UserID, &item.AudiobookID, &startedAt, &endedAt,
			&item.StartPositionSeconds, &item.EndPositionSeconds, &item.DurationSeconds, &completed); err != nil {
			return nil, err
		}
		item.Completed = completed != 0
		item.StartedAt = parseTimePtr(startedAt)
		item.EndedAt = parseTimePtr(endedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}
