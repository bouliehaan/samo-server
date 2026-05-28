package playlists

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
)

// FirstAdminOwnerID returns the first admin user's id for filesystem playlist import.
func FirstAdminOwnerID(ctx context.Context, db *sql.DB) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `
		SELECT id FROM users WHERE role = 'admin' ORDER BY created_at LIMIT 1`).Scan(&id)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(id), nil
}

// ImportM3UFromPath imports or updates a playlist from an on-disk M3U/M3U8 file.
func (s *Service) ImportM3UFromPath(ctx context.Context, ownerID, path string) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrDisabled
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return false, ErrForbidden
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return false, ErrInvalidInput
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	replace := true
	_, err = s.Import(ctx, ownerID, ImportInput{
		Name:       name,
		SourceType: "m3u",
		Content:    string(data),
		Replace:    &replace,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}
