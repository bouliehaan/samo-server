package users

import (
	"context"
	"database/sql"
	"strings"
)

type BootstrapInput struct {
	AdminUsername string
	AdminPassword string
}

type BootstrapResult struct {
	AdminUsername      string
	CreatedAdmin       bool
	GeneratedPassword  string
	UpdatedPassword    bool
	EnsuredServerToken bool
}

func bootstrap(ctx context.Context, db *sql.DB, service *Service, input BootstrapInput) (BootstrapResult, error) {
	var result BootstrapResult
	serverUser, err := loadUserByID(ctx, db, BootstrapUserID)
	if err != nil {
		return BootstrapResult{}, err
	}

	if service.legacyAPIToken != "" {
		if err := ensureServerToken(ctx, db, service.legacyTokenHash); err != nil {
			return BootstrapResult{}, err
		}
		result.EnsuredServerToken = true
	}

	username := normalizeUsername(input.AdminUsername)
	if username == "" {
		username = "admin"
	}
	result.AdminUsername = username
	if username == serverUser.Username {
		if password := strings.TrimSpace(input.AdminPassword); password != "" {
			hash, err := hashPassword(password)
			if err != nil {
				return BootstrapResult{}, err
			}
			if err := updateUserRecord(ctx, db, BootstrapUserID, nil, &hash); err != nil {
				return BootstrapResult{}, err
			}
			result.UpdatedPassword = true
		}
		return result, nil
	}

	password := strings.TrimSpace(input.AdminPassword)
	existing, _, err := loadUserByUsername(ctx, db, username)
	if err == nil {
		if password != "" && existing.Role == RoleAdmin {
			hash, err := hashPassword(password)
			if err != nil {
				return BootstrapResult{}, err
			}
			if err := updateUserRecord(ctx, db, existing.ID, nil, &hash); err != nil {
				return BootstrapResult{}, err
			}
			result.UpdatedPassword = true
		}
		return result, nil
	}
	if err != nil && err != ErrNotFound {
		return BootstrapResult{}, err
	}

	count, err := countUsers(ctx, db)
	if err != nil {
		return BootstrapResult{}, err
	}
	if count > 1 {
		return result, nil
	}

	if password == "" && strings.TrimSpace(input.AdminUsername) == "" {
		// No explicit username and no password — defer creation to the
		// /setup wizard so a human picks the credentials in the UI.
		return result, nil
	}
	if password == "" {
		password, err = newBootstrapPassword()
		if err != nil {
			return BootstrapResult{}, err
		}
		result.GeneratedPassword = password
	}

	item, err := validateCreateInput(CreateUserInput{
		Username: username,
		Password: password,
		Role:     RoleAdmin,
	})
	if err != nil {
		return BootstrapResult{}, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := insertUser(ctx, db, item, hash); err != nil {
		return BootstrapResult{}, err
	}
	result.CreatedAdmin = true
	return result, nil
}

func ensureServerToken(ctx context.Context, db *sql.DB, tokenHash string) error {
	var existing string
	err := db.QueryRowContext(ctx, `
		SELECT id FROM user_tokens WHERE user_id = ? AND label = ?`,
		BootstrapUserID, serverTokenLabel,
	).Scan(&existing)
	if err == nil {
		_, err = db.ExecContext(ctx, `UPDATE user_tokens SET token_hash = ? WHERE id = ?`, tokenHash, existing)
		return err
	}
	if err != sql.ErrNoRows {
		return err
	}
	return insertToken(ctx, db, "token-server", BootstrapUserID, serverTokenLabel, tokenHash)
}
