package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type CreateBookmarkInput struct {
	Title           string `json:"title,omitempty"`
	Note            string `json:"note,omitempty"`
	PositionSeconds int    `json:"positionSeconds"`
	ChapterID       string `json:"chapterId,omitempty"`
}

type UpdateBookmarkInput struct {
	Title           *string `json:"title,omitempty"`
	Note            *string `json:"note,omitempty"`
	PositionSeconds *int    `json:"positionSeconds,omitempty"`
	ChapterID       *string `json:"chapterId,omitempty"`
}

func (s *Service) ListBookmarks(ctx context.Context, userID, audiobookID string) ([]Bookmark, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	audiobookID = strings.TrimSpace(audiobookID)
	if userID == "" || audiobookID == "" {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, audiobook_id, title, note, position_seconds, chapter_id, created_at, updated_at
		FROM bookmarks
		WHERE user_id = ? AND audiobook_id = ?
		ORDER BY position_seconds ASC, created_at ASC`, userID, audiobookID)
	if err != nil {
		return nil, fmt.Errorf("list bookmarks: %w", err)
	}
	defer rows.Close()
	return scanBookmarks(rows)
}

// ListUserBookmarks returns every bookmark the user has saved across all
// audiobooks, most-recent first. Used by GET /api/v1/bookmarks for the
// dashboard "your bookmarks" view.
func (s *Service) ListUserBookmarks(ctx context.Context, userID string, limit int) ([]Bookmark, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, ErrInvalidInput
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, audiobook_id, title, note, position_seconds, chapter_id, created_at, updated_at
		FROM bookmarks
		WHERE user_id = ?
		ORDER BY updated_at DESC, created_at DESC
		LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list user bookmarks: %w", err)
	}
	defer rows.Close()
	return scanBookmarks(rows)
}

func (s *Service) CreateBookmark(ctx context.Context, userID, audiobookID string, input CreateBookmarkInput) (Bookmark, error) {
	if s == nil || s.db == nil {
		return Bookmark{}, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	audiobookID = strings.TrimSpace(audiobookID)
	if userID == "" || audiobookID == "" {
		return Bookmark{}, ErrInvalidInput
	}
	if err := assertAudiobookExists(ctx, s.db, audiobookID); err != nil {
		return Bookmark{}, err
	}
	if input.PositionSeconds < 0 {
		return Bookmark{}, ErrInvalidInput
	}
	id := stableID("bookmark", userID, audiobookID, fmt.Sprint(input.PositionSeconds), input.ChapterID, nowRFC3339())
	now := nowRFC3339()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bookmarks (
		  id, user_id, audiobook_id, title, note, position_seconds, chapter_id, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, audiobookID, strings.TrimSpace(input.Title), strings.TrimSpace(input.Note),
		input.PositionSeconds, nullableString(input.ChapterID), now, now)
	if err != nil {
		return Bookmark{}, fmt.Errorf("create bookmark: %w", err)
	}
	return s.loadBookmark(ctx, userID, id)
}

func (s *Service) UpdateBookmark(ctx context.Context, userID, id string, input UpdateBookmarkInput) (Bookmark, error) {
	current, err := s.loadBookmark(ctx, userID, id)
	if err != nil {
		return Bookmark{}, err
	}
	if current.UserID != userID {
		return Bookmark{}, ErrForbidden
	}
	title := current.Title
	if input.Title != nil {
		title = strings.TrimSpace(*input.Title)
	}
	note := current.Note
	if input.Note != nil {
		note = strings.TrimSpace(*input.Note)
	}
	position := current.PositionSeconds
	if input.PositionSeconds != nil {
		if *input.PositionSeconds < 0 {
			return Bookmark{}, ErrInvalidInput
		}
		position = *input.PositionSeconds
	}
	chapterID := current.ChapterID
	if input.ChapterID != nil {
		chapterID = strings.TrimSpace(*input.ChapterID)
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE bookmarks
		SET title = ?, note = ?, position_seconds = ?, chapter_id = ?, updated_at = ?
		WHERE id = ? AND user_id = ?`,
		title, note, position, nullableString(chapterID), nowRFC3339(), id, userID)
	if err != nil {
		return Bookmark{}, fmt.Errorf("update bookmark: %w", err)
	}
	return s.loadBookmark(ctx, userID, id)
}

func (s *Service) DeleteBookmark(ctx context.Context, userID, id string) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM bookmarks WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete bookmark: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) loadBookmark(ctx context.Context, userID, id string) (Bookmark, error) {
	var item Bookmark
	var chapterID sql.NullString
	var createdAt, updatedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, audiobook_id, title, note, position_seconds, chapter_id, created_at, updated_at
		FROM bookmarks
		WHERE id = ?`, id).
		Scan(&item.ID, &item.UserID, &item.AudiobookID, &item.Title, &item.Note, &item.PositionSeconds,
			&chapterID, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return Bookmark{}, ErrNotFound
	}
	if err != nil {
		return Bookmark{}, fmt.Errorf("load bookmark: %w", err)
	}
	if strings.TrimSpace(userID) != "" && item.UserID != userID {
		return Bookmark{}, ErrForbidden
	}
	item.ChapterID = chapterID.String
	item.CreatedAt = parseTimePtr(createdAt)
	item.UpdatedAt = parseTimePtr(updatedAt)
	return item, nil
}

func scanBookmarks(rows *sql.Rows) ([]Bookmark, error) {
	var items []Bookmark
	for rows.Next() {
		var item Bookmark
		var chapterID sql.NullString
		var createdAt, updatedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.UserID, &item.AudiobookID, &item.Title, &item.Note, &item.PositionSeconds,
			&chapterID, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.ChapterID = chapterID.String
		item.CreatedAt = parseTimePtr(createdAt)
		item.UpdatedAt = parseTimePtr(updatedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func nullableString(value string) sql.NullString {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}
