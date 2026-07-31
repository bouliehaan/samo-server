package scannerstore

import (
	"context"
	"fmt"

	"github.com/bouliehaan/samo-server/internal/storage"
)

// UpsertLibrary writes the library row, preserving the id of an existing row
// that already occupies the path.
//
// Two conflicts are possible and they mean different things. ON CONFLICT(id)
// is the ordinary re-upsert of a library we already know. A UNIQUE violation
// on path means a row exists there under a *different* id — created via the
// API and then re-synced from env vars, or rehashed by a migration. Adopting
// the existing id keeps every media_files row still pointing at a live
// library; inserting a second row would orphan them.
func (s *Store) UpsertLibrary(ctx context.Context, id, name, kind, mediaType, path string) error {
	_, err := s.exec(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  kind = excluded.kind,
		  media_type = excluded.media_type,
		  path = excluded.path,
		  updated_at = CURRENT_TIMESTAMP`,
		id, name, kind, mediaType, path)
	if err == nil {
		return nil
	}
	if !storage.IsUniqueViolation(err) {
		return fmt.Errorf("upsert library %q: %w", path, err)
	}
	if _, err := s.exec(ctx, `
		UPDATE libraries
		SET name = ?, kind = ?, media_type = ?, updated_at = CURRENT_TIMESTAMP
		WHERE path = ?`,
		name, kind, mediaType, path); err != nil {
		return fmt.Errorf("update library by path %q: %w", path, err)
	}
	return nil
}
