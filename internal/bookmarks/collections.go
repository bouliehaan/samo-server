package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type CreateCollectionInput struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Public       bool     `json:"public"`
	AudiobookIDs []string `json:"audiobookIds,omitempty"`
}

type UpdateCollectionInput struct {
	Name         *string  `json:"name,omitempty"`
	Description  *string  `json:"description,omitempty"`
	Public       *bool    `json:"public,omitempty"`
	AudiobookIDs []string `json:"audiobookIds,omitempty"`
}

func (s *Service) ListCollections(ctx context.Context, userID string) ([]Collection, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, ErrInvalidInput
	}
	rows, err := s.dbForRead().QueryContext(ctx, `
		SELECT id, user_id, name, description, public, created_at, updated_at
		FROM collections
		WHERE user_id = ?
		ORDER BY name ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()
	var items []Collection
	for rows.Next() {
		item, err := scanCollectionRow(rows)
		if err != nil {
			return nil, err
		}
		item.AudiobookIDs, err = s.loadCollectionAudiobookIDs(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		item.AudiobookCount = len(item.AudiobookIDs)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) GetCollection(ctx context.Context, userID, id string) (Collection, error) {
	return s.loadCollection(ctx, userID, id)
}

func (s *Service) CreateCollection(ctx context.Context, userID string, input CreateCollectionInput) (Collection, error) {
	if s == nil || s.db == nil {
		return Collection{}, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	name := strings.TrimSpace(input.Name)
	if userID == "" || name == "" {
		return Collection{}, ErrInvalidInput
	}
	audiobookIDs, err := s.validateCollectionAudiobooks(ctx, input.AudiobookIDs)
	if err != nil {
		return Collection{}, err
	}
	id := stableID("collection", userID, name, nowRFC3339())
	now := nowRFC3339()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Collection{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO collections (id, user_id, name, description, public, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, userID, name, strings.TrimSpace(input.Description), boolInt(input.Public), now, now); err != nil {
		return Collection{}, fmt.Errorf("create collection: %w", err)
	}
	if err := replaceCollectionAudiobooks(ctx, tx, id, audiobookIDs); err != nil {
		return Collection{}, err
	}
	if err := tx.Commit(); err != nil {
		return Collection{}, err
	}
	return s.loadCollection(ctx, userID, id)
}

func (s *Service) UpdateCollection(ctx context.Context, userID, id string, input UpdateCollectionInput) (Collection, error) {
	current, err := s.loadCollection(ctx, userID, id)
	if err != nil {
		return Collection{}, err
	}
	if current.UserID != userID {
		return Collection{}, ErrForbidden
	}
	name := current.Name
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
		if name == "" {
			return Collection{}, ErrInvalidInput
		}
	}
	description := current.Description
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	public := current.Public
	if input.Public != nil {
		public = *input.Public
	}
	audiobookIDs := current.AudiobookIDs
	if input.AudiobookIDs != nil {
		audiobookIDs, err = s.validateCollectionAudiobooks(ctx, input.AudiobookIDs)
		if err != nil {
			return Collection{}, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Collection{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE collections
		SET name = ?, description = ?, public = ?, updated_at = ?
		WHERE id = ? AND user_id = ?`,
		name, description, boolInt(public), nowRFC3339(), id, userID); err != nil {
		return Collection{}, fmt.Errorf("update collection: %w", err)
	}
	if input.AudiobookIDs != nil {
		if err := replaceCollectionAudiobooks(ctx, tx, id, audiobookIDs); err != nil {
			return Collection{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Collection{}, err
	}
	return s.loadCollection(ctx, userID, id)
}

func (s *Service) DeleteCollection(ctx context.Context, userID, id string) error {
	current, err := s.loadCollection(ctx, userID, id)
	if err != nil {
		return err
	}
	if current.UserID != userID {
		return ErrForbidden
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM collections WHERE id = ?`, id)
	return err
}

func (s *Service) validateCollectionAudiobooks(ctx context.Context, audiobookIDs []string) ([]string, error) {
	seen := map[string]struct{}{}
	valid := make([]string, 0, len(audiobookIDs))
	for _, audiobookID := range audiobookIDs {
		audiobookID = strings.TrimSpace(audiobookID)
		if audiobookID == "" {
			continue
		}
		if _, ok := seen[audiobookID]; ok {
			continue
		}
		seen[audiobookID] = struct{}{}
		if err := assertAudiobookExists(ctx, s.db, audiobookID); err != nil {
			return nil, err
		}
		valid = append(valid, audiobookID)
	}
	return valid, nil
}

func replaceCollectionAudiobooks(ctx context.Context, tx *sql.Tx, collectionID string, audiobookIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM collection_audiobooks WHERE collection_id = ?`, collectionID); err != nil {
		return fmt.Errorf("clear collection audiobooks: %w", err)
	}
	for index, audiobookID := range audiobookIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO collection_audiobooks (collection_id, audiobook_id, position, added_at)
			VALUES (?, ?, ?, ?)`,
			collectionID, audiobookID, index, nowRFC3339()); err != nil {
			return fmt.Errorf("insert collection audiobook: %w", err)
		}
	}
	return nil
}

func (s *Service) loadCollection(ctx context.Context, userID, id string) (Collection, error) {
	var item Collection
	var public int
	var createdAt, updatedAt sql.NullString
	err := s.dbForRead().QueryRowContext(ctx, `
		SELECT id, user_id, name, description, public, created_at, updated_at
		FROM collections
		WHERE id = ?`, id).
		Scan(&item.ID, &item.UserID, &item.Name, &item.Description, &public, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return Collection{}, ErrNotFound
	}
	if err != nil {
		return Collection{}, fmt.Errorf("load collection: %w", err)
	}
	if strings.TrimSpace(userID) != "" && item.UserID != userID {
		return Collection{}, ErrForbidden
	}
	item.Public = public != 0
	item.CreatedAt = parseTimePtr(createdAt)
	item.UpdatedAt = parseTimePtr(updatedAt)
	item.AudiobookIDs, err = s.loadCollectionAudiobookIDs(ctx, item.ID)
	if err != nil {
		return Collection{}, err
	}
	item.AudiobookCount = len(item.AudiobookIDs)
	return item, nil
}

func (s *Service) loadCollectionAudiobookIDs(ctx context.Context, collectionID string) ([]string, error) {
	rows, err := s.dbForRead().QueryContext(ctx, `
		SELECT audiobook_id
		FROM collection_audiobooks
		WHERE collection_id = ?
		ORDER BY position ASC`, collectionID)
	if err != nil {
		return nil, fmt.Errorf("load collection audiobooks: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func scanCollectionRow(rows *sql.Rows) (Collection, error) {
	var item Collection
	var public int
	var createdAt, updatedAt sql.NullString
	if err := rows.Scan(&item.ID, &item.UserID, &item.Name, &item.Description, &public, &createdAt, &updatedAt); err != nil {
		return Collection{}, err
	}
	item.Public = public != 0
	item.CreatedAt = parseTimePtr(createdAt)
	item.UpdatedAt = parseTimePtr(updatedAt)
	return item, nil
}
