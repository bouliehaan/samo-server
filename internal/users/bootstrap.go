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
	// RetiredServerToken is a start without SAMO_API_TOKEN deleting the shared
	// token an earlier start was configured with, so its secret no longer
	// authenticates.
	RetiredServerToken bool
}

func bootstrap(ctx context.Context, db *sql.DB, service *Service, input BootstrapInput) (BootstrapResult, error) {
	var result BootstrapResult
	// Guarantee the reserved bootstrap row exists before anything reads it. On
	// SQLite this duplicates migration 008 (harmless); on a fresh Postgres
	// database — whose schema is generated structurally, without seed DML — this
	// is what creates 'user-server' so loadUserByID and the server-token FK below
	// don't fail on first boot.
	if err := ensureReservedServerUser(ctx, db); err != nil {
		return BootstrapResult{}, err
	}
	serverUser, err := loadUserByID(ctx, db, BootstrapUserID)
	if err != nil {
		return BootstrapResult{}, err
	}

	if service.legacyAPIToken != "" {
		if err := ensureServerToken(ctx, db, service.legacyTokenHash); err != nil {
			return BootstrapResult{}, err
		}
		result.EnsuredServerToken = true
	} else {
		retired, err := retireServerToken(ctx, db)
		if err != nil {
			return BootstrapResult{}, err
		}
		result.RetiredServerToken = retired
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

// ensureReservedServerUser upserts the reserved 'user-server' account. The
// INSERT ... ON CONFLICT DO NOTHING form is portable across SQLite and Postgres
// and never clobbers an existing row (e.g. one a human renamed).
func ensureReservedServerUser(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, role, password_hash)
		VALUES (?, 'server', 'Server', 'admin', '')
		ON CONFLICT (id) DO NOTHING`, BootstrapUserID)
	return err
}

// isServerToken reports whether a token row is the one ensureServerToken
// keeps: the legacy SAMO_API_TOKEN, one secret shared by every client and
// script of an older install. The row, not the configured secret, is what
// identifies it, the same way ensureServerToken and retireServerToken find it.
// Other tokens on the reserved account (samo-radio devices, someone signed in
// as `server`) are ordinary tokens.
func isServerToken(userID, label string) bool {
	return userID == BootstrapUserID && label == serverTokenLabel
}

// ensureServerToken and retireServerToken keep the row in step with
// SAMO_API_TOKEN at every start: written when it is first set, rewritten when
// it changes (so a rotated secret stops working), and deleted when it is
// removed.
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

// retireServerToken deletes the shared token once SAMO_API_TOKEN is no longer
// set. Taking the variable out of the environment is how an operator withdraws
// the secret, and the row it left behind would otherwise go on authenticating
// it with nothing in the configuration to show that it still works. Only that
// row: the reserved account's other tokens, samo-radio devices among them, are
// untouched.
func retireServerToken(ctx context.Context, db *sql.DB) (bool, error) {
	result, err := db.ExecContext(ctx, `
		DELETE FROM user_tokens WHERE user_id = ? AND label = ?`,
		BootstrapUserID, serverTokenLabel,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}
