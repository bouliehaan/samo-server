package users

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// parseStoredTime accepts both RFC3339 strings written by Go and the
// space-separated `YYYY-MM-DD HH:MM:SS` format SQLite produces from
// CURRENT_TIMESTAMP. Returns zero time if neither parses — callers can
// treat that as "unknown".
func parseStoredTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func loadUserByID(ctx context.Context, db *sql.DB, id string) (User, error) {
	var item User
	var createdAt, updatedAt string
	err := db.QueryRowContext(ctx, `
		SELECT id, username, display_name, role, created_at, updated_at
		FROM users WHERE id = ?`, id).Scan(
		&item.ID, &item.Username, &item.DisplayName, &item.Role, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load user: %w", err)
	}
	item.CreatedAt = parseStoredTime(createdAt)
	item.UpdatedAt = parseStoredTime(updatedAt)
	return item, nil
}

func loadUserByUsername(ctx context.Context, db *sql.DB, username string) (User, string, error) {
	var item User
	var passwordHash, createdAt, updatedAt string
	err := db.QueryRowContext(ctx, `
		SELECT id, username, display_name, role, password_hash, created_at, updated_at
		FROM users WHERE username = ? COLLATE NOCASE`, username).Scan(
		&item.ID, &item.Username, &item.DisplayName, &item.Role, &passwordHash, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return User{}, "", ErrNotFound
	}
	if err != nil {
		return User{}, "", fmt.Errorf("load user: %w", err)
	}
	item.CreatedAt = parseStoredTime(createdAt)
	item.UpdatedAt = parseStoredTime(updatedAt)
	return item, passwordHash, nil
}

func insertUser(ctx context.Context, db *sql.DB, item User, passwordHash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, role, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.Username, item.DisplayName, item.Role, passwordHash, now, now,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrUsernameTaken
		}
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func updateUserRecord(ctx context.Context, db *sql.DB, id string, displayName *string, passwordHash *string) error {
	if displayName == nil && passwordHash == nil {
		return nil
	}
	setParts := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := make([]any, 0, 4)
	if displayName != nil {
		setParts = append(setParts, "display_name = ?")
		args = append(args, strings.TrimSpace(*displayName))
	}
	if passwordHash != nil {
		setParts = append(setParts, "password_hash = ?")
		args = append(args, *passwordHash)
	}
	args = append(args, id)
	query := fmt.Sprintf("UPDATE users SET %s WHERE id = ?", strings.Join(setParts, ", "))
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
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

func listUsers(ctx context.Context, db *sql.DB) ([]User, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, username, display_name, role, created_at, updated_at
		FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]User, 0)
	for rows.Next() {
		var item User
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.Username, &item.DisplayName, &item.Role, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func insertToken(ctx context.Context, db *sql.DB, id, userID, label, tokenHash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx, `
		INSERT INTO user_tokens (id, user_id, label, token_hash, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, userID, label, tokenHash, now,
	)
	return err
}

func loadUserByTokenHash(ctx context.Context, db *sql.DB, tokenHash string) (User, string, error) {
	var userID, tokenID string
	err := db.QueryRowContext(ctx, `
		SELECT user_id, id FROM user_tokens WHERE token_hash = ?`, tokenHash).Scan(&userID, &tokenID)
	if err == sql.ErrNoRows {
		return User{}, "", ErrInvalidToken
	}
	if err != nil {
		return User{}, "", err
	}
	user, err := loadUserByID(ctx, db, userID)
	if err != nil {
		return User{}, "", err
	}
	_, _ = db.ExecContext(ctx, `UPDATE user_tokens SET last_used_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), tokenID)
	return user, tokenID, nil
}

func listTokens(ctx context.Context, db *sql.DB, userID string) ([]Token, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, label, created_at, last_used_at
		FROM user_tokens WHERE user_id = ?
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Token, 0)
	for rows.Next() {
		var item Token
		var createdAt string
		var lastUsed sql.NullString
		if err := rows.Scan(&item.ID, &item.Label, &createdAt, &lastUsed); err != nil {
			return nil, err
		}
		item.CreatedAt = parseStoredTime(createdAt)
		if lastUsed.Valid {
			parsed := parseStoredTime(lastUsed.String)
			if !parsed.IsZero() {
				item.LastUsedAt = &parsed
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func deleteToken(ctx context.Context, db *sql.DB, userID, tokenID string) error {
	result, err := db.ExecContext(ctx, `DELETE FROM user_tokens WHERE id = ? AND user_id = ?`, tokenID, userID)
	if err != nil {
		return err
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

func countUsers(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}
