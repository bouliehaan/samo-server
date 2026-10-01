package users

import (
	"context"
	"strings"
)

// Serialize account changes in the database, including concurrent admin removals.
// The reserved server identity does not count as a human administrator.
func (s *Service) manageUser(ctx context.Context, actor Principal, id string, input UpdateUserInput, remove bool) (User, error) {
	if id == BootstrapUserID {
		return User{}, ErrProtectedUser
	}
	if id == actor.User.ID && (remove || (input.Role != nil && *input.Role != actor.User.Role)) {
		return User{}, ErrProtectedUser
	}
	var passwordHash *string
	if input.Password != nil {
		if strings.TrimSpace(*input.Password) == "" || len(*input.Password) > 72 {
			return User{}, ErrInvalidPassword
		}
		hash, err := hashPassword(*input.Password)
		if err != nil {
			return User{}, err
		}
		passwordHash = &hash
	}
	if input.Username != nil {
		normalized := normalizeUsername(*input.Username)
		if normalized == "" {
			return User{}, ErrInvalidUsername
		}
		input.Username = &normalized
	}
	if input.Role != nil && *input.Role != RoleAdmin && *input.Role != RoleUser {
		return User{}, ErrInvalidRole
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, role, password_hash FROM users ORDER BY id FOR UPDATE`)
	if err != nil {
		return User{}, err
	}
	var found, targetAdmin, actorAdmin bool
	admins := 0
	for rows.Next() {
		var userID, role, hash string
		if err := rows.Scan(&userID, &role, &hash); err != nil {
			rows.Close()
			return User{}, err
		}
		if userID == actor.User.ID {
			actorAdmin = role == RoleAdmin
		}
		humanAdmin := userID != BootstrapUserID && role == RoleAdmin && hash != ""
		if humanAdmin {
			admins++
		}
		if userID == id {
			found = true
			targetAdmin = humanAdmin
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return User{}, err
	}
	if !found {
		return User{}, ErrNotFound
	}
	if (remove || id != actor.User.ID || input.Role != nil || input.Username != nil) && !actorAdmin {
		return User{}, ErrForbidden
	}
	if targetAdmin && (remove || (input.Role != nil && *input.Role != RoleAdmin)) && admins <= 1 {
		return User{}, ErrLastAdmin
	}
	if remove {
		_, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	} else {
		parts := []string{"updated_at = CURRENT_TIMESTAMP"}
		args := []any{}
		for _, field := range []struct {
			name  string
			value *string
		}{{"username", input.Username}, {"display_name", input.DisplayName}, {"role", input.Role}, {"password_hash", passwordHash}} {
			if field.value != nil {
				parts = append(parts, field.name+" = ?")
				args = append(args, strings.TrimSpace(*field.value))
			}
		}
		args = append(args, id)
		_, err = tx.ExecContext(ctx, "UPDATE users SET "+strings.Join(parts, ", ")+" WHERE id = ?", args...)
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, ErrUsernameTaken
		}
		return User{}, err
	}
	// Administrative resets retire existing sessions and app passwords. A user
	// changing their own password keeps the current session usable.
	if passwordHash != nil && actor.User.ID != id {
		if _, err = tx.ExecContext(ctx, `DELETE FROM user_tokens WHERE user_id = ?`, id); err != nil {
			return User{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE users SET subsonic_password = '' WHERE id = ?`, id); err != nil {
			return User{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	if remove || (passwordHash != nil && actor.User.ID != id) {
		s.streamTokens.revokeUser(id)
	}
	if remove {
		return User{}, nil
	}
	return loadUserByID(ctx, s.db, id)
}
