package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/log"
)

// External sign-in (OIDC, LDAP) proves who someone is somewhere else. What it
// opens here is still a samo account: the same users row, the same bearer
// tokens, the same plays and settings a password login reaches. This file is
// the one place that decides which account an outside identity is.
//
// An identity is found by (provider, subject), never by username. A username
// is what a person is called today; the subject is what the provider promises
// never to hand to anyone else. Matching on username is offered only as a way
// to adopt accounts that existed before the provider did (LinkByUsername), and
// only the first time — after that the link is by subject like any other.

// ExternalIdentity is who an identity provider says has just signed in.
type ExternalIdentity struct {
	// Provider is "oidc:<issuer>" or "ldap".
	Provider string
	// Subject is the provider's stable, never-reused id for this person.
	Subject     string
	Username    string
	DisplayName string
	Email       string
	Groups      []string
}

// ExternalPolicy is what an admin decided external sign-in may do.
type ExternalPolicy struct {
	// AutoProvision creates a samo account for an identity that has none.
	// Off, an admin creates accounts and links them, or LinkByUsername does.
	AutoProvision bool
	// LinkByUsername adopts an existing, unlinked samo account whose username
	// matches the identity's on its first sign-in. Only safe when the provider
	// does not let people choose their own usernames.
	LinkByUsername bool
	// AdminGroup makes its members admins and everyone else users, on every
	// sign-in. Empty leaves roles to the Users page.
	AdminGroup string
	// RequiredGroup refuses anyone outside it. Empty admits whoever the
	// provider lets through.
	RequiredGroup string
}

// Identity is a linked external identity as the Users page shows it.
type Identity struct {
	Provider    string    `json:"provider"`
	Subject     string    `json:"subject"`
	Username    string    `json:"username"`
	Email       string    `json:"email,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	LastLoginAt time.Time `json:"lastLoginAt"`
}

var (
	ErrNotProvisioned   = errors.New("no samo account is linked to this sign-in; ask an admin to create one")
	ErrNotInGroup       = errors.New("this account is not in the group allowed to use samo")
	ErrIdentityConflict = errors.New("a samo account with this username already exists and is not linked to this sign-in; ask an admin to link it")
	ErrInvalidIdentity  = errors.New("the identity provider did not say who signed in")
)

// ResolveExternal finds or makes the samo account for an identity the caller
// has already verified, applies the policy's group rules, and records the
// sign-in. It does not issue a token; IssueLogin does.
func (s *Service) ResolveExternal(ctx context.Context, identity ExternalIdentity, policy ExternalPolicy) (Principal, error) {
	if !s.Enabled() {
		return Principal{}, ErrDisabled
	}
	identity.Provider = strings.TrimSpace(identity.Provider)
	identity.Subject = strings.TrimSpace(identity.Subject)
	if identity.Provider == "" || identity.Subject == "" {
		return Principal{}, ErrInvalidIdentity
	}
	if group := strings.TrimSpace(policy.RequiredGroup); group != "" && !InGroup(identity.Groups, group) {
		return Principal{}, ErrNotInGroup
	}

	userID, err := s.linkedUser(ctx, identity)
	if errors.Is(err, ErrNotFound) {
		userID, err = s.adoptOrProvision(ctx, identity, policy)
		if errors.Is(err, ErrIdentityConflict) {
			// The account with this name may be the one a concurrent first
			// sign-in of this same identity has just made and linked.
			userID, err = s.lostLinkRace(ctx, identity, err)
		}
	}
	if err != nil {
		return Principal{}, err
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE user_identities SET username = ?, email = ?, last_login_at = NOW()
		WHERE provider = ? AND subject = ?`,
		strings.TrimSpace(identity.Username), strings.TrimSpace(identity.Email), identity.Provider, identity.Subject); err != nil {
		return Principal{}, fmt.Errorf("record sign-in: %w", err)
	}
	if group := strings.TrimSpace(policy.AdminGroup); group != "" {
		role := RoleUser
		if InGroup(identity.Groups, group) {
			role = RoleAdmin
		}
		if err := s.syncRole(ctx, userID, role); err != nil {
			return Principal{}, err
		}
	}
	user, err := loadUserByID(ctx, s.db, userID)
	if err != nil {
		return Principal{}, err
	}
	return Principal{User: user}, nil
}

func (s *Service) linkedUser(ctx context.Context, identity ExternalIdentity) (string, error) {
	var userID string
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id FROM user_identities WHERE provider = ? AND subject = ?`,
		identity.Provider, identity.Subject).Scan(&userID)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load identity: %w", err)
	}
	return userID, nil
}

// adoptOrProvision links an identity seen for the first time: to the existing
// account of the same name when the policy allows it, or to a new one.
func (s *Service) adoptOrProvision(ctx context.Context, identity ExternalIdentity, policy ExternalPolicy) (string, error) {
	username := externalUsername(identity.Username)
	if username == "" {
		username = externalUsername(strings.SplitN(identity.Email, "@", 2)[0])
	}
	if username == "" {
		return "", ErrInvalidUsername
	}

	existing, _, err := loadUserByUsername(ctx, s.db, username)
	switch {
	case err == nil:
		if !policy.LinkByUsername || existing.ID == BootstrapUserID {
			return "", ErrIdentityConflict
		}
		// An account already linked to another identity from this same
		// provider is someone else's: a directory reusing a freed name must
		// not inherit the previous holder's library.
		var others int
		if err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM user_identities WHERE user_id = ? AND provider = ?`,
			existing.ID, identity.Provider).Scan(&others); err != nil {
			return "", err
		}
		if others > 0 {
			return "", ErrIdentityConflict
		}
		if err := s.insertIdentity(ctx, s.db, existing.ID, identity); err != nil {
			return s.lostLinkRace(ctx, identity, err)
		}
		log.Infof("users: linked %s identity %q to existing account %s", identity.Provider, identity.Username, existing.Username)
		return existing.ID, nil
	case !errors.Is(err, ErrNotFound):
		return "", err
	}

	if !policy.AutoProvision {
		return "", ErrNotProvisioned
	}
	id, err := newUserID()
	if err != nil {
		return "", err
	}
	displayName := strings.TrimSpace(identity.DisplayName)
	if displayName == "" {
		displayName = username
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	// No password: password_hash stays empty, which verifyPassword never
	// accepts, so this account opens only through its provider (or a
	// password an admin or the person sets later).
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, role, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', ?, ?)`, id, username, displayName, RoleUser, now, now); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			// Someone took the name between the lookup and here — most often
			// this same identity, signing in twice at once.
			return s.lostLinkRace(ctx, identity, ErrIdentityConflict)
		}
		return "", fmt.Errorf("provision user: %w", err)
	}
	if err := s.insertIdentity(ctx, tx, id, identity); err != nil {
		tx.Rollback()
		return s.lostLinkRace(ctx, identity, err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	log.Infof("users: created account %s for %s identity %q", username, identity.Provider, identity.Username)
	return id, nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Service) insertIdentity(ctx context.Context, db execer, userID string, identity ExternalIdentity) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO user_identities (provider, subject, user_id, username, email)
		VALUES (?, ?, ?, ?, ?)`,
		identity.Provider, identity.Subject, userID, strings.TrimSpace(identity.Username), strings.TrimSpace(identity.Email))
	if err != nil {
		return fmt.Errorf("link identity: %w", err)
	}
	return nil
}

// lostLinkRace answers a failed link: if the identity is linked now, a
// concurrent sign-in of the same person won and its account is the answer.
func (s *Service) lostLinkRace(ctx context.Context, identity ExternalIdentity, cause error) (string, error) {
	if userID, err := s.linkedUser(ctx, identity); err == nil {
		return userID, nil
	}
	return "", cause
}

// syncRole moves an account to the role its groups give it. The rule
// manageUser keeps — at least one admin who can sign in with a password —
// holds here too, so a directory change cannot lock the break-glass account
// out of the server.
func (s *Service) syncRole(ctx context.Context, userID, role string) error {
	if userID == BootstrapUserID {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, role, password_hash FROM users ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	var current string
	var targetHumanAdmin bool
	admins := 0
	for rows.Next() {
		var id, r, hash string
		if err := rows.Scan(&id, &r, &hash); err != nil {
			rows.Close()
			return err
		}
		humanAdmin := id != BootstrapUserID && r == RoleAdmin && hash != ""
		if humanAdmin {
			admins++
		}
		if id == userID {
			current = r
			targetHumanAdmin = humanAdmin
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if current == role || current == "" {
		return nil
	}
	if targetHumanAdmin && role != RoleAdmin && admins <= 1 {
		log.Warnf("users: kept %s an admin although the identity provider no longer says so: it is the last admin with a password", userID)
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, role, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// IssueLogin mints the bearer a sign-in hands its client, in the shape a
// password login has always returned.
func (s *Service) IssueLogin(ctx context.Context, principal Principal, label string) (LoginResponse, error) {
	issued, err := s.IssueToken(ctx, principal, CreateTokenInput{Label: label})
	if err != nil {
		return LoginResponse{}, err
	}
	return LoginResponse{User: principal.User, Token: issued.Secret, TokenMeta: issued.Token}, nil
}

// ListIdentities is every external identity linked to an account.
func (s *Service) ListIdentities(ctx context.Context, userID string) ([]Identity, error) {
	rows, err := s.dbForRead().QueryContext(ctx, `
		SELECT provider, subject, username, email, created_at, last_login_at
		FROM user_identities WHERE user_id = ? ORDER BY provider, subject`, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Identity, 0)
	for rows.Next() {
		var item Identity
		if err := rows.Scan(&item.Provider, &item.Subject, &item.Username, &item.Email, &item.CreatedAt, &item.LastLoginAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// UnlinkIdentity detaches an external identity from an account. The account
// and its tokens stay; that identity's next sign-in is a first one again.
func (s *Service) UnlinkIdentity(ctx context.Context, actor Principal, userID, provider, subject string) error {
	if actor.User.Role != RoleAdmin {
		return ErrForbidden
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM user_identities WHERE user_id = ? AND provider = ? AND subject = ?`,
		strings.TrimSpace(userID), strings.TrimSpace(provider), strings.TrimSpace(subject))
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// InGroup reports whether want is among groups. A directory names a group by
// its DN (cn=samo-admins,ou=groups,dc=example,dc=org) where an OIDC provider
// names it plainly, so a DN also matches its leading RDN's value — the same
// name an admin types in either case.
func InGroup(groups []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if strings.EqualFold(group, want) {
			return true
		}
		if rdn, _, ok := strings.Cut(group, ","); ok {
			if _, value, ok := strings.Cut(rdn, "="); ok && strings.EqualFold(strings.TrimSpace(value), want) {
				return true
			}
		}
	}
	return false
}

// externalUsername makes a samo username from a provider's: lower case, and
// anything samo usernames cannot hold (dots, @, spaces) becomes a dash.
func externalUsername(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	dash := false
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
			dash = r == '-'
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 64 {
		name = strings.Trim(name[:64], "-")
	}
	return normalizeUsername(name)
}
