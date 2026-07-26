package playlists

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/bouliehaan/samo-server/internal/users"
)

// FirstAdminOwnerID returns the admin who should own server-managed playlists
// (filesystem imports, the explo drop playlist). The internal bootstrap
// account (users.BootstrapUserID) has a zero created_at so a naive
// ORDER BY created_at always picks it - but a non-public playlist it owns is
// invisible to every real user, since playlists only render for their owner.
// Prefer the earliest HUMAN admin; fall back to the bootstrap account only
// when no human admin exists.
func FirstAdminOwnerID(ctx context.Context, db *sql.DB) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `
		SELECT id FROM users WHERE role = 'admin'
		ORDER BY (id = ?), created_at LIMIT 1`, users.BootstrapUserID).Scan(&id)
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
	// A tombstoned name means an owner or admin deleted this playlist on
	// purpose; the scan pass must not resurrect it from the on-disk file.
	// (A manual API import of the same name clears the tombstone.)
	tombstoned, err := s.nameTombstoned(ctx, name)
	if err != nil {
		return false, err
	}
	if tombstoned {
		return false, nil
	}
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
