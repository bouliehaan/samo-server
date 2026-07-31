package users

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// Subsonic client credentials.
//
// The protocol offers two auth modes and real clients overwhelmingly default to
// the second:
//
//	p=<plaintext>            or  p=enc:<hex>
//	t=md5(password+salt) & s=<salt>
//
// The token mode can only be verified by a server that can recover the
// plaintext password. Samo's login passwords are bcrypt hashes, which by design
// it cannot. Rather than weaken that — Navidrome stores passwords reversibly
// precisely to make this work — Subsonic gets a separate generated app
// password. Login stays bcrypt; Subsonic access is opt-in per user, revocable,
// and grants nothing beyond browsing and streaming that user's library.

// SubsonicEnabled reports whether the user has a Subsonic credential set.
func (s *Service) SubsonicEnabled(ctx context.Context, userID string) (bool, error) {
	if !s.Enabled() {
		return false, ErrDisabled
	}
	password, err := loadSubsonicPassword(ctx, s.dbForRead(), userID)
	if err != nil {
		return false, err
	}
	return password != "", nil
}

// GenerateSubsonicPassword mints (or rotates) the caller's Subsonic credential
// and returns it. It is shown once at the point of creation the same way an API
// token is; the user pastes it into their client alongside their username.
func (s *Service) GenerateSubsonicPassword(ctx context.Context, actor Principal) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}
	password, err := newAPIToken()
	if err != nil {
		return "", err
	}
	if err := setSubsonicPassword(ctx, s.db, actor.User.ID, password); err != nil {
		return "", err
	}
	return password, nil
}

// ClearSubsonicPassword revokes Subsonic access for the caller.
func (s *Service) ClearSubsonicPassword(ctx context.Context, actor Principal) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	return setSubsonicPassword(ctx, s.db, actor.User.ID, "")
}

// AuthenticateSubsonic resolves a Subsonic request's credentials to a user.
//
// It accepts either auth mode against the Subsonic app password, and also
// accepts the account's real password in the `p` field — some clients only
// offer plaintext, and a user who typed their login password there should not
// be met with a silent failure. It never accepts the login password via
// token+salt, because that would require storing it recoverably.
func (s *Service) AuthenticateSubsonic(ctx context.Context, username, password, token, salt string) (Principal, error) {
	if !s.Enabled() {
		return Principal{}, ErrDisabled
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return Principal{}, ErrUnauthorized
	}

	user, passwordHash, err := loadUserByUsername(ctx, s.dbForRead(), username)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	subsonicPassword, err := loadSubsonicPassword(ctx, s.dbForRead(), user.ID)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}

	// Token + salt: only the Subsonic app password can satisfy this.
	if token != "" && salt != "" {
		if subsonicPassword == "" {
			return Principal{}, ErrUnauthorized
		}
		sum := md5.Sum([]byte(subsonicPassword + salt))
		expected := hex.EncodeToString(sum[:])
		if subtle.ConstantTimeCompare([]byte(strings.ToLower(token)), []byte(expected)) != 1 {
			return Principal{}, ErrUnauthorized
		}
		return Principal{User: user}, nil
	}

	// Plaintext (or hex-encoded) password.
	plain, err := decodeSubsonicPassword(password)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	if plain == "" {
		return Principal{}, ErrUnauthorized
	}
	if subsonicPassword != "" &&
		subtle.ConstantTimeCompare([]byte(plain), []byte(subsonicPassword)) == 1 {
		return Principal{User: user}, nil
	}
	if verifyPassword(passwordHash, plain) {
		return Principal{User: user}, nil
	}
	return Principal{}, ErrUnauthorized
}

// decodeSubsonicPassword unwraps the protocol's `enc:<hex>` obfuscation, which
// several clients use by default. It is not encryption and is not treated as
// such; it exists so a password does not sit in plain sight in a URL.
func decodeSubsonicPassword(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "enc:") {
		return raw, nil
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(raw, "enc:"))
	if err != nil {
		return "", fmt.Errorf("decode enc: password: %w", err)
	}
	return string(decoded), nil
}

func loadSubsonicPassword(ctx context.Context, db *sql.DB, userID string) (string, error) {
	var password string
	err := db.QueryRowContext(ctx,
		`SELECT subsonic_password FROM users WHERE id = ?`, strings.TrimSpace(userID)).Scan(&password)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load subsonic password: %w", err)
	}
	return password, nil
}

func setSubsonicPassword(ctx context.Context, db *sql.DB, userID, password string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE users SET subsonic_password = ? WHERE id = ?`, password, strings.TrimSpace(userID))
	if err != nil {
		return fmt.Errorf("set subsonic password: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	return nil
}
