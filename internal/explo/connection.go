package explo

import (
	"context"
	"database/sql"
	"time"
)

// RegisteredRemote resolves the callback registered by an authenticated Explo
// instance. This credential is never included in user-facing configuration.
func RegisteredRemote(ctx context.Context, db *sql.DB) (*Remote, error) {
	if db == nil {
		return nil, nil
	}
	var baseURL, token string
	err := db.QueryRowContext(ctx, `SELECT base_url, token FROM explo_connection WHERE id = 1`).Scan(&baseURL, &token)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return NewRemote(baseURL, token), nil
}
func SaveConnection(ctx context.Context, db *sql.DB, baseURL, token string) error {
	if db == nil {
		return ErrDisabled
	}
	if NewRemote(baseURL, token) == nil {
		return ErrInvalidConfig
	}
	_, err := db.ExecContext(ctx, `INSERT INTO explo_connection (id, base_url, token, updated_at)
 VALUES (1, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET
 base_url = excluded.base_url, token = excluded.token, updated_at = excluded.updated_at`, baseURL, token, time.Now().UTC().Format(time.RFC3339))
	return err
}
