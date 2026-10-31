// Package extauth signs people in through an identity provider samo does not
// run: OpenID Connect (authentik's OAuth2/OpenID provider, Keycloak, Authelia,
// …) and LDAP (authentik's LDAP outpost, OpenLDAP, lldap, …).
//
// It only proves who someone is. Which samo account that makes them, and what
// that account may do, is internal/users (ResolveExternal); handing a browser
// or a device its bearer afterwards is internal/api. Nothing here issues a
// token, and nothing here changes how a bearer is checked — an SSO sign-in
// ends in exactly the token a password sign-in does, so every client, the
// Subsonic adapter and the stream-token rules work unchanged.
package extauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/users"
)

var (
	ErrDisabled      = errors.New("this sign-in method is not enabled")
	ErrInvalidConfig = errors.New("invalid sign-in provider settings")
	ErrUnauthorized  = errors.New("unauthorized")
)

// Settings is everything an admin configures on the sign-in providers panel.
// Stored as one JSON row (auth_provider_config), secrets included.
type Settings struct {
	OIDC OIDCSettings `json:"oidc"`
	LDAP LDAPSettings `json:"ldap"`

	// PublicURL is the address browsers reach samo at, e.g.
	// https://samo.example.com. The OIDC redirect URI is built from it, so it
	// must match what is registered at the provider. Empty falls back to the
	// address the sign-in request arrived on, which is right on a LAN and
	// wrong behind a proxy that rewrites Host.
	PublicURL string `json:"publicUrl"`

	// The account policy both providers share; see users.ExternalPolicy.
	AutoProvision  bool   `json:"autoProvision"`
	LinkByUsername bool   `json:"linkByUsername"`
	AdminGroup     string `json:"adminGroup"`
	RequiredGroup  string `json:"requiredGroup"`
}

type OIDCSettings struct {
	Enabled bool `json:"enabled"`
	// Label names the provider on the sign-in button: "authentik".
	Label string `json:"label"`
	// Issuer is the provider's issuer URL. For authentik it is the
	// application's: https://auth.example.com/application/o/samo/
	Issuer       string `json:"issuer"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
	// Scopes requested beyond openid. Default "profile email"; authentik's
	// profile scope already carries groups.
	Scopes        string `json:"scopes"`
	UsernameClaim string `json:"usernameClaim"`
	GroupsClaim   string `json:"groupsClaim"`
	// CACert is extra PEM roots to trust when samo itself talks to the
	// provider (discovery, token, keys, userinfo) — for a provider behind a
	// private CA or a Cloudflare Origin certificate. There is deliberately no
	// skip-verify: that channel carries the keys that prove who signed in.
	CACert string `json:"caCert,omitempty"`
}

type LDAPSettings struct {
	Enabled bool `json:"enabled"`
	// URL is ldap://host:389 or ldaps://host:636. authentik's outpost listens
	// on 3389 and 6636 by default.
	URL      string `json:"url"`
	StartTLS bool   `json:"startTls"`
	// InsecureSkipVerify accepts any certificate. As unsafe as plain ldap://,
	// and offered for the same reason: a LAN-only outpost with a self-signed
	// certificate. CACert is the better answer.
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
	CACert             string `json:"caCert,omitempty"`
	// BindDN and BindPassword are the service account that searches for the
	// person signing in. authentik: cn=<service account>,ou=users,<base DN>.
	BindDN       string `json:"bindDn"`
	BindPassword string `json:"bindPassword,omitempty"`
	BaseDN       string `json:"baseDn"`
	// UserFilter finds the entry for a username; {username} is replaced,
	// escaped. The default suits authentik, where cn is the username.
	UserFilter      string `json:"userFilter"`
	UsernameAttr    string `json:"usernameAttr"`
	DisplayNameAttr string `json:"displayNameAttr"`
	EmailAttr       string `json:"emailAttr"`
	GroupsAttr      string `json:"groupsAttr"`
	// SubjectAttr is the entry's never-reused id. Empty tries entryUUID, then
	// uid (authentik's own unique id), then falls back to the DN.
	SubjectAttr string `json:"subjectAttr"`
}

const (
	defaultScopes          = "profile email"
	defaultUsernameClaim   = "preferred_username"
	defaultGroupsClaim     = "groups"
	defaultOIDCLabel       = "single sign-on"
	defaultUserFilter      = "(&(objectClass=user)(cn={username}))"
	defaultUsernameAttr    = "cn"
	defaultDisplayNameAttr = "displayName"
	defaultEmailAttr       = "mail"
	defaultGroupsAttr      = "memberOf"
)

// withDefaults fills every field an admin may leave blank.
func (s Settings) withDefaults() Settings {
	o := &s.OIDC
	o.Label = orDefault(o.Label, defaultOIDCLabel)
	o.Scopes = orDefault(o.Scopes, defaultScopes)
	o.UsernameClaim = orDefault(o.UsernameClaim, defaultUsernameClaim)
	o.GroupsClaim = orDefault(o.GroupsClaim, defaultGroupsClaim)
	l := &s.LDAP
	l.UserFilter = orDefault(l.UserFilter, defaultUserFilter)
	l.UsernameAttr = orDefault(l.UsernameAttr, defaultUsernameAttr)
	l.DisplayNameAttr = orDefault(l.DisplayNameAttr, defaultDisplayNameAttr)
	l.EmailAttr = orDefault(l.EmailAttr, defaultEmailAttr)
	l.GroupsAttr = orDefault(l.GroupsAttr, defaultGroupsAttr)
	return s
}

func orDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

// Policy is the account policy in the shape internal/users takes.
func (s Settings) Policy() users.ExternalPolicy {
	return users.ExternalPolicy{
		AutoProvision:  s.AutoProvision,
		LinkByUsername: s.LinkByUsername,
		AdminGroup:     strings.TrimSpace(s.AdminGroup),
		RequiredGroup:  strings.TrimSpace(s.RequiredGroup),
	}
}

// OIDCReady reports whether OIDC is switched on and has what it needs.
func (s Settings) OIDCReady() bool {
	return s.OIDC.Enabled && strings.TrimSpace(s.OIDC.Issuer) != "" && strings.TrimSpace(s.OIDC.ClientID) != ""
}

// LDAPReady reports whether LDAP is switched on and has what it needs.
func (s Settings) LDAPReady() bool {
	return s.LDAP.Enabled && strings.TrimSpace(s.LDAP.URL) != "" && strings.TrimSpace(s.LDAP.BaseDN) != ""
}

// Validate refuses settings that cannot work, so the panel says so on save
// rather than on someone's first failed sign-in.
func (s Settings) Validate() error {
	if u := strings.TrimSpace(s.PublicURL); u != "" {
		parsed, err := url.Parse(u)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("%w: public URL must be an http(s) address like https://samo.example.com", ErrInvalidConfig)
		}
	}
	if s.OIDC.Enabled {
		parsed, err := url.Parse(strings.TrimSpace(s.OIDC.Issuer))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("%w: OIDC issuer must be an http(s) URL", ErrInvalidConfig)
		}
		if strings.TrimSpace(s.OIDC.ClientID) == "" {
			return fmt.Errorf("%w: OIDC client ID is required", ErrInvalidConfig)
		}
		if _, err := tlsConfig(s.OIDC.CACert, false); err != nil {
			return err
		}
	}
	if s.LDAP.Enabled {
		parsed, err := url.Parse(strings.TrimSpace(s.LDAP.URL))
		if err != nil || (parsed.Scheme != "ldap" && parsed.Scheme != "ldaps") || parsed.Host == "" {
			return fmt.Errorf("%w: LDAP URL must look like ldap://host:389 or ldaps://host:636", ErrInvalidConfig)
		}
		if strings.TrimSpace(s.LDAP.BaseDN) == "" {
			return fmt.Errorf("%w: LDAP base DN is required", ErrInvalidConfig)
		}
		if !strings.Contains(s.withDefaults().LDAP.UserFilter, "{username}") {
			return fmt.Errorf("%w: LDAP user filter must contain {username}", ErrInvalidConfig)
		}
		if _, err := tlsConfig(s.LDAP.CACert, false); err != nil {
			return err
		}
	}
	return nil
}

// Public is the settings as an admin's browser may see them: every secret
// reduced to whether it is set.
type Public struct {
	Settings
	HasClientSecret bool       `json:"hasClientSecret"`
	HasBindPassword bool       `json:"hasBindPassword"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
}

func (s Settings) public(updatedAt *time.Time) Public {
	out := Public{
		Settings:        s,
		HasClientSecret: strings.TrimSpace(s.OIDC.ClientSecret) != "",
		HasBindPassword: s.LDAP.BindPassword != "",
		UpdatedAt:       updatedAt,
	}
	out.OIDC.ClientSecret = ""
	out.LDAP.BindPassword = ""
	return out
}

// Store keeps the settings row and a copy of it, so a sign-in does not cost a
// query to learn which providers exist.
type Store struct {
	db *sql.DB

	mu        sync.RWMutex
	loaded    bool
	settings  Settings
	updatedAt *time.Time
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Settings is the current settings, defaults filled in.
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	s.mu.RLock()
	if s.loaded {
		settings := s.settings
		s.mu.RUnlock()
		return settings.withDefaults(), nil
	}
	s.mu.RUnlock()
	if err := s.load(ctx); err != nil {
		return Settings{}, err
	}
	return s.Settings(ctx)
}

// Public is the current settings with secrets withheld.
func (s *Store) Public(ctx context.Context) (Public, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return Public{}, err
	}
	s.mu.RLock()
	updatedAt := s.updatedAt
	s.mu.RUnlock()
	return settings.public(updatedAt), nil
}

func (s *Store) load(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	var raw []byte
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx, `SELECT settings, updated_at FROM auth_provider_config WHERE id = 1`).Scan(&raw, &updatedAt)
	var settings Settings
	var at *time.Time
	switch {
	case err == sql.ErrNoRows:
	case err != nil:
		return fmt.Errorf("load sign-in provider settings: %w", err)
	default:
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("decode sign-in provider settings: %w", err)
		}
		at = &updatedAt
	}
	s.mu.Lock()
	s.settings, s.updatedAt, s.loaded = settings, at, true
	s.mu.Unlock()
	return nil
}

// Save replaces the settings. A blank secret keeps the stored one, the way the
// Last.fm panel keeps its shared secret, so an admin can change the issuer
// without retyping the client secret.
func (s *Store) Save(ctx context.Context, next Settings) (Public, error) {
	if s == nil || s.db == nil {
		return Public{}, ErrDisabled
	}
	current, err := s.Settings(ctx)
	if err != nil {
		return Public{}, err
	}
	if strings.TrimSpace(next.OIDC.ClientSecret) == "" {
		next.OIDC.ClientSecret = current.OIDC.ClientSecret
	}
	if next.LDAP.BindPassword == "" {
		next.LDAP.BindPassword = current.LDAP.BindPassword
	}
	next.PublicURL = strings.TrimRight(strings.TrimSpace(next.PublicURL), "/")
	next.OIDC.Issuer = strings.TrimSpace(next.OIDC.Issuer)
	next.OIDC.ClientID = strings.TrimSpace(next.OIDC.ClientID)
	next.OIDC.ClientSecret = strings.TrimSpace(next.OIDC.ClientSecret)
	next.LDAP.URL = strings.TrimSpace(next.LDAP.URL)
	if err := next.Validate(); err != nil {
		return Public{}, err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return Public{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_provider_config (id, settings, updated_at) VALUES (1, ?::jsonb, NOW())
		ON CONFLICT (id) DO UPDATE SET settings = excluded.settings, updated_at = excluded.updated_at`, string(raw)); err != nil {
		return Public{}, fmt.Errorf("save sign-in provider settings: %w", err)
	}
	if err := s.load(ctx); err != nil {
		return Public{}, err
	}
	return s.Public(ctx)
}

// tlsConfig builds the client TLS settings for a provider: the system roots
// plus any PEM the admin pasted.
func tlsConfig(caPEM string, insecure bool) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure}
	if strings.TrimSpace(caPEM) == "" {
		return config, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, fmt.Errorf("%w: the CA certificate is not PEM (-----BEGIN CERTIFICATE-----)", ErrInvalidConfig)
	}
	config.RootCAs = pool
	return config, nil
}
