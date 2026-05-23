package shelfuser

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type CreateCollectionInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Public      bool     `json:"public"`
	ItemIDs     []string `json:"itemIds,omitempty"`
}

type UpdateCollectionInput struct {
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Public      *bool    `json:"public,omitempty"`
	ItemIDs     []string `json:"itemIds,omitempty"`
}

func (s *Service) ListCollections(ctx context.Context, userID string) ([]Collection, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, description, public, created_at, updated_at
		FROM shelf_collections
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
		item.ItemIDs, err = s.loadCollectionItemIDs(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		item.ItemCount = len(item.ItemIDs)
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
	itemIDs, err := s.validateCollectionItems(ctx, input.ItemIDs)
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
		INSERT INTO shelf_collections (id, user_id, name, description, public, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, userID, name, strings.TrimSpace(input.Description), boolInt(input.Public), now, now); err != nil {
		return Collection{}, fmt.Errorf("create collection: %w", err)
	}
	if err := replaceCollectionItems(ctx, tx, id, itemIDs); err != nil {
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
	itemIDs := current.ItemIDs
	if input.ItemIDs != nil {
		itemIDs, err = s.validateCollectionItems(ctx, input.ItemIDs)
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
		UPDATE shelf_collections
		SET name = ?, description = ?, public = ?, updated_at = ?
		WHERE id = ? AND user_id = ?`,
		name, description, boolInt(public), nowRFC3339(), id, userID); err != nil {
		return Collection{}, fmt.Errorf("update collection: %w", err)
	}
	if input.ItemIDs != nil {
		if err := replaceCollectionItems(ctx, tx, id, itemIDs); err != nil {
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
	_, err = s.db.ExecContext(ctx, `DELETE FROM shelf_collections WHERE id = ?`, id)
	return err
}

func (s *Service) validateCollectionItems(ctx context.Context, itemIDs []string) ([]string, error) {
	seen := map[string]struct{}{}
	valid := make([]string, 0, len(itemIDs))
	for _, itemID := range itemIDs {
		itemID = strings.TrimSpace(itemID)
		if itemID == "" {
			continue
		}
		if _, ok := seen[itemID]; ok {
			continue
		}
		seen[itemID] = struct{}{}
		if err := assertAudiobookItem(ctx, s.db, itemID); err != nil {
			return nil, err
		}
		valid = append(valid, itemID)
	}
	return valid, nil
}

func replaceCollectionItems(ctx context.Context, tx *sql.Tx, collectionID string, itemIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM shelf_collection_items WHERE collection_id = ?`, collectionID); err != nil {
		return fmt.Errorf("clear collection items: %w", err)
	}
	for index, itemID := range itemIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO shelf_collection_items (collection_id, item_id, position, added_at)
			VALUES (?, ?, ?, ?)`,
			collectionID, itemID, index, nowRFC3339()); err != nil {
			return fmt.Errorf("insert collection item: %w", err)
		}
	}
	return nil
}

func (s *Service) loadCollection(ctx context.Context, userID, id string) (Collection, error) {
	var item Collection
	var public int
	var createdAt, updatedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, description, public, created_at, updated_at
		FROM shelf_collections
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
	item.ItemIDs, err = s.loadCollectionItemIDs(ctx, item.ID)
	if err != nil {
		return Collection{}, err
	}
	item.ItemCount = len(item.ItemIDs)
	return item, nil
}

func (s *Service) loadCollectionItemIDs(ctx context.Context, collectionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT item_id
		FROM shelf_collection_items
		WHERE collection_id = ?
		ORDER BY position ASC`, collectionID)
	if err != nil {
		return nil, fmt.Errorf("load collection items: %w", err)
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
