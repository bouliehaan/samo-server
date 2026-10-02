package users

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/log"
)

type Service struct {
	db              *sql.DB
	readDB          *sql.DB
	legacyAPIToken  string
	legacyTokenHash string
	streamTokens    *streamTokenStore

	touchMu   sync.Mutex
	touchedAt map[string]time.Time // token id → when its last write was dispatched
	touches   sync.WaitGroup       // in-flight last_used_at writes
}

type ServiceOptions struct {
	DB             *sql.DB
	ReadDB         *sql.DB
	LegacyAPIToken string
}

func New(options ServiceOptions) *Service {
	token := strings.TrimSpace(options.LegacyAPIToken)
	readDB := options.ReadDB
	if readDB == nil {
		readDB = options.DB
	}
	return &Service{
		db:              options.DB,
		readDB:          readDB,
		legacyAPIToken:  token,
		legacyTokenHash: hashToken(token),
		streamTokens:    newStreamTokenStore(),
	}
}

func (s *Service) dbForRead() *sql.DB {
	if s.readDB != nil {
		return s.readDB
	}
	return s.db
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
	user, err := loadUserByID(ctx, s.dbForRead(), userID)
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
		user, err := loadUserByID(ctx, s.dbForRead(), BootstrapUserID)
		if err != nil {
			return Principal{}, err
		}
		return Principal{User: user}, nil
	}
	user, tokenID, lastUsed, err := loadUserByTokenHash(ctx, s.dbForRead(), hashToken(token))
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	s.noteTokenUse(tokenID, lastUsed)
	return Principal{User: user}, nil
}

// tokenTouchInterval is how stale a token's last_used_at may get before a use
// rewrites it. The column answers "is this token still in use?", which a
// minute's resolution answers fine, and clients authenticate on every progress
// PATCH and stream open — writing each one would make reads cost writes.
const tokenTouchInterval = time.Minute

// tokenTouchTimeout bounds one background last_used_at write.
const tokenTouchTimeout = 30 * time.Second

// noteTokenUse records a use of tokenID in last_used_at once it has gone
// tokenTouchInterval stale. Authentication reads through the read-only pool
// and must not wait on the write pool — that pool is the one a scan saturates,
// which is why reads have their own — so the write runs in the background.
// touchedAt holds back a second write for the same token within the interval
// while the first one is still queued, which the stored value alone can't.
func (s *Service) noteTokenUse(tokenID string, lastUsed time.Time) {
	now := time.Now().UTC()
	if now.Sub(lastUsed) < tokenTouchInterval {
		return
	}
	s.touchMu.Lock()
	if now.Sub(s.touchedAt[tokenID]) < tokenTouchInterval {
		s.touchMu.Unlock()
		return
	}
	if s.touchedAt == nil {
		s.touchedAt = make(map[string]time.Time)
	}
	for id, at := range s.touchedAt {
		if now.Sub(at) >= tokenTouchInterval {
			delete(s.touchedAt, id)
		}
	}
	s.touchedAt[tokenID] = now
	s.touchMu.Unlock()

	s.touches.Add(1)
	go func() {
		defer s.touches.Done()
		ctx, cancel := context.WithTimeout(context.Background(), tokenTouchTimeout)
		defer cancel()
		if err := touchToken(ctx, s.db, tokenID, now); err != nil {
			log.Warnf("users: record use of token %s: %v", tokenID, err)
		}
	}()
}

func (s *Service) AuthenticateCredentials(ctx context.Context, username, password string) (Principal, error) {
	if !s.Enabled() {
		return Principal{}, ErrDisabled
	}
	user, passwordHash, err := loadUserByUsername(ctx, s.dbForRead(), strings.TrimSpace(username))
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	if !verifyPassword(passwordHash, password) {
		return Principal{}, ErrUnauthorized
	}
	return Principal{User: user}, nil
}

func (s *Service) Get(ctx context.Context, id string) (User, error) {
	return loadUserByID(ctx, s.dbForRead(), strings.TrimSpace(id))
}

func (s *Service) GetByUsername(ctx context.Context, username string) (User, error) {
	user, _, err := loadUserByUsername(ctx, s.dbForRead(), strings.TrimSpace(username))
	return user, err
}

func (s *Service) List(ctx context.Context) ([]User, error) {
	return listUsers(ctx, s.dbForRead())
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
	if input.Username != nil || input.Role != nil {
		if actor.User.Role != RoleAdmin {
			return User{}, ErrForbidden
		}
	}
	return s.manageUser(ctx, actor, targetID, input, false)
}

func (s *Service) Delete(ctx context.Context, actor Principal, targetID string) error {
	if actor.User.Role != RoleAdmin {
		return ErrForbidden
	}
	_, err := s.manageUser(ctx, actor, strings.TrimSpace(targetID), UpdateUserInput{}, true)
	return err
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
	tokens, err := listTokens(ctx, s.dbForRead(), actor.User.ID)
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
	return listTokens(ctx, s.dbForRead(), actor.User.ID)
}

func (s *Service) RevokeToken(ctx context.Context, actor Principal, tokenID string) error {
	return deleteToken(ctx, s.db, actor.User.ID, strings.TrimSpace(tokenID))
}

// RevokePresentedToken is sign-out: it retires the token whose secret the
// caller presents, which has to be one of the actor's own. A client holds its
// token's secret and never its id, and the secret is all it takes to find the
// row.
//
// The shared SAMO_API_TOKEN is refused. It is one secret for every legacy
// client and script of an install at once, so one of them signing out must
// not cut off the rest.
func (s *Service) RevokePresentedToken(ctx context.Context, actor Principal, secret string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ErrUnauthorized
	}
	if s.legacyAPIToken != "" && secret == s.legacyAPIToken {
		return ErrSharedToken
	}
	tokenID, owner, label, err := loadTokenByHash(ctx, s.db, hashToken(secret))
	if err == ErrInvalidToken {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if isServerToken(owner, label) {
		return ErrSharedToken
	}
	if owner != actor.User.ID {
		return ErrForbidden
	}
	return deleteToken(ctx, s.db, owner, tokenID)
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
