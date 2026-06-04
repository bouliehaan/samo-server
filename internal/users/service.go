package users

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type Service struct {
	db              *sql.DB
	legacyAPIToken  string
	legacyTokenHash string
	streamTokens    *streamTokenStore
}

type ServiceOptions struct {
	DB             *sql.DB
	LegacyAPIToken string
}

func New(options ServiceOptions) *Service {
	token := strings.TrimSpace(options.LegacyAPIToken)
	return &Service{
		db:              options.DB,
		legacyAPIToken:  token,
		legacyTokenHash: hashToken(token),
		streamTokens:    newStreamTokenStore(),
	}
}

func (s *Service) Enabled() bool {
	return s != nil && s.db != nil
}

// IssueStreamToken mints an ephemeral credential the dashboard can paste into
// <audio src=...> URLs without leaking the long-lived bearer token. The
// caller must already be authenticated.
func (s *Service) IssueStreamToken(userID string) (string, time.Time, error) {
	if !s.Enabled() {
		return "", time.Time{}, ErrDisabled
	}
	if strings.TrimSpace(userID) == "" {
		return "", time.Time{}, ErrUnauthorized
	}
	return s.streamTokens.issue(strings.TrimSpace(userID))
}

// AuthenticateStreamToken resolves an ephemeral stream token to the user it
// was minted for. Used by the request auth path when a route accepts
// ?stream_token=... in place of a bearer header.
func (s *Service) AuthenticateStreamToken(ctx context.Context, token string) (Principal, error) {
	if !s.Enabled() {
		return Principal{}, ErrDisabled
	}
	userID, ok := s.streamTokens.validate(strings.TrimSpace(token))
	if !ok {
		return Principal{}, ErrUnauthorized
	}
	user, err := loadUserByID(ctx, s.db, userID)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	return Principal{User: user}, nil
}

func (s *Service) Bootstrap(ctx context.Context, input BootstrapInput) error {
	_, err := s.BootstrapWithResult(ctx, input)
	return err
}

func (s *Service) BootstrapWithResult(ctx context.Context, input BootstrapInput) (BootstrapResult, error) {
	if !s.Enabled() {
		return BootstrapResult{}, ErrDisabled
	}
	return bootstrap(ctx, s.db, s, input)
}

func (s *Service) AuthenticateToken(ctx context.Context, token string) (Principal, error) {
	if !s.Enabled() {
		return Principal{}, ErrDisabled
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return Principal{}, ErrUnauthorized
	}
	if s.legacyAPIToken != "" && token == s.legacyAPIToken {
		user, err := loadUserByID(ctx, s.db, BootstrapUserID)
		if err != nil {
			return Principal{}, err
		}
		return Principal{User: user}, nil
	}
	user, _, err := loadUserByTokenHash(ctx, s.db, hashToken(token))
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	return Principal{User: user}, nil
}

func (s *Service) AuthenticateCredentials(ctx context.Context, username, password string) (Principal, error) {
	if !s.Enabled() {
		return Principal{}, ErrDisabled
	}
	user, passwordHash, err := loadUserByUsername(ctx, s.db, strings.TrimSpace(username))
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	if !verifyPassword(passwordHash, password) {
		return Principal{}, ErrUnauthorized
	}
	return Principal{User: user}, nil
}

func (s *Service) Get(ctx context.Context, id string) (User, error) {
	return loadUserByID(ctx, s.db, strings.TrimSpace(id))
}

func (s *Service) GetByUsername(ctx context.Context, username string) (User, error) {
	user, _, err := loadUserByUsername(ctx, s.db, strings.TrimSpace(username))
	return user, err
}

func (s *Service) List(ctx context.Context) ([]User, error) {
	return listUsers(ctx, s.db)
}

func (s *Service) Create(ctx context.Context, actor Principal, input CreateUserInput) (User, error) {
	if actor.User.Role != RoleAdmin {
		return User{}, ErrForbidden
	}
	item, err := validateCreateInput(input)
	if err != nil {
		return User{}, err
	}
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		return User{}, err
	}
	if err := insertUser(ctx, s.db, item, passwordHash); err != nil {
		return User{}, err
	}
	return loadUserByID(ctx, s.db, item.ID)
}

func (s *Service) Update(ctx context.Context, actor Principal, targetID string, input UpdateUserInput) (User, error) {
	targetID = strings.TrimSpace(targetID)
	if actor.User.ID != targetID && actor.User.Role != RoleAdmin {
		return User{}, ErrForbidden
	}
	var passwordHash *string
	if input.Password != nil {
		if strings.TrimSpace(*input.Password) == "" {
			return User{}, ErrInvalidPassword
		}
		hash, err := hashPassword(*input.Password)
		if err != nil {
			return User{}, err
		}
		passwordHash = &hash
	}
	if err := updateUserRecord(ctx, s.db, targetID, input.DisplayName, passwordHash); err != nil {
		return User{}, err
	}
	return loadUserByID(ctx, s.db, targetID)
}

func (s *Service) Login(ctx context.Context, input LoginInput) (LoginResponse, error) {
	principal, err := s.AuthenticateCredentials(ctx, input.Username, input.Password)
	if err != nil {
		return LoginResponse{}, err
	}
	issued, err := s.IssueToken(ctx, principal, CreateTokenInput{Label: "login"})
	if err != nil {
		return LoginResponse{}, err
	}
	return LoginResponse{
		User:      principal.User,
		Token:     issued.Secret,
		TokenMeta: issued.Token,
	}, nil
}

func (s *Service) IssueToken(ctx context.Context, actor Principal, input CreateTokenInput) (TokenIssue, error) {
	secret, err := newAPIToken()
	if err != nil {
		return TokenIssue{}, err
	}
	label := strings.TrimSpace(input.Label)
	if label == "" {
		label = "api"
	}
	tokenID, err := newUserID()
	if err != nil {
		return TokenIssue{}, err
	}
	tokenID = "token-" + strings.TrimPrefix(tokenID, "user-")
	if err := insertToken(ctx, s.db, tokenID, actor.User.ID, label, hashToken(secret)); err != nil {
		return TokenIssue{}, err
	}
	tokens, err := listTokens(ctx, s.db, actor.User.ID)
	if err != nil {
		return TokenIssue{}, err
	}
	var meta Token
	for _, item := range tokens {
		if item.ID == tokenID {
			meta = item
			break
		}
	}
	return TokenIssue{Token: meta, Secret: secret}, nil
}

func (s *Service) ListTokens(ctx context.Context, actor Principal) ([]Token, error) {
	return listTokens(ctx, s.db, actor.User.ID)
}

func (s *Service) RevokeToken(ctx context.Context, actor Principal, tokenID string) error {
	return deleteToken(ctx, s.db, actor.User.ID, strings.TrimSpace(tokenID))
}

func validateCreateInput(input CreateUserInput) (User, error) {
	username := normalizeUsername(input.Username)
	if username == "" {
		return User{}, ErrInvalidUsername
	}
	if strings.TrimSpace(input.Password) == "" {
		return User{}, ErrInvalidPassword
	}
	role := strings.TrimSpace(input.Role)
	if role == "" {
		role = RoleUser
	}
	if role != RoleAdmin && role != RoleUser {
		role = RoleUser
	}
	id, err := newUserID()
	if err != nil {
		return User{}, err
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = username
	}
	return User{
		ID:          id,
		Username:    username,
		DisplayName: displayName,
		Role:        role,
	}, nil
}

func normalizeUsername(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" || len(raw) > 64 {
		return ""
	}
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return ""
	}
	return raw
}
