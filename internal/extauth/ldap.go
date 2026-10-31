package extauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/bouliehaan/samo-server/internal/users"
)

// LDAP sign-in is the classic search-then-bind:
//
//  1. Bind as the service account and search the base DN for the one entry
//     the user filter finds for the username typed.
//  2. Bind as that entry with the password typed. The directory, not samo,
//     decides whether it is right.
//  3. Read the entry's username, name, mail and groups into an identity.
//
// It runs behind the ordinary password form and the same /auth/login
// endpoint, so every client that signs in with a username and password —
// the web app, the desktop and Android apps, a TV's pairing approval — gets it
// without knowing LDAP exists.

const ldapTimeout = 10 * time.Second

// ldapSubjectFallbacks are tried in order when SubjectAttr is not set.
var ldapSubjectFallbacks = []string{"entryUUID", "uid"}

// LDAPProviderID is the user_identities.provider LDAP identities carry.
const LDAPProviderID = "ldap"

// ldapConn is the part of *ldap.Conn this uses, so tests can stand in a fake
// directory.
type ldapConn interface {
	Bind(username, password string) error
	Search(request *ldap.SearchRequest) (*ldap.SearchResult, error)
	Close() error
}

// LDAP authenticates against the configured directory.
type LDAP struct {
	dial func(ctx context.Context, settings LDAPSettings) (ldapConn, error)
}

func NewLDAP() *LDAP {
	return &LDAP{dial: dialLDAP}
}

func dialLDAP(ctx context.Context, settings LDAPSettings) (ldapConn, error) {
	config, err := tlsConfig(settings.CACert, settings.InsecureSkipVerify)
	if err != nil {
		return nil, err
	}
	address := strings.TrimSpace(settings.URL)
	if host := ldapHost(address); host != "" {
		config.ServerName = host
	}
	dialer := &net.Dialer{Timeout: ldapTimeout}
	conn, err := ldap.DialURL(address, ldap.DialWithDialer(dialer), ldap.DialWithTLSConfig(config))
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", address, err)
	}
	conn.SetTimeout(ldapTimeout)
	if settings.StartTLS && strings.HasPrefix(strings.ToLower(address), "ldap://") {
		if err := conn.StartTLS(config); err != nil {
			conn.Close()
			return nil, fmt.Errorf("StartTLS with %s: %w", address, err)
		}
	}
	return conn, nil
}

func ldapHost(address string) string {
	rest := address
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	rest = strings.SplitN(rest, "/", 2)[0]
	if host, _, err := net.SplitHostPort(rest); err == nil {
		return host
	}
	return rest
}

// Authenticate checks a username and password against the directory.
// ErrUnauthorized means the directory said no (no such entry, wrong
// password); any other error means it could not be asked.
func (l *LDAP) Authenticate(ctx context.Context, settings LDAPSettings, username, password string) (users.ExternalIdentity, error) {
	username = strings.TrimSpace(username)
	// An empty password is an unauthenticated bind (RFC 4513 §5.1.2), which
	// many directories answer with success. It must never reach Bind.
	if username == "" || password == "" {
		return users.ExternalIdentity{}, ErrUnauthorized
	}
	conn, err := l.dial(ctx, settings)
	if err != nil {
		return users.ExternalIdentity{}, err
	}
	defer conn.Close()

	entry, err := l.find(conn, settings, username)
	if err != nil {
		return users.ExternalIdentity{}, err
	}
	if err := conn.Bind(entry.DN, password); err != nil {
		// 49 is a wrong password. 50 is the directory refusing this person
		// (authentik answers it for anyone outside the application's bound
		// group). Both are a "no", not an outage.
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) ||
			ldap.IsErrorWithCode(err, ldap.LDAPResultInsufficientAccessRights) {
			return users.ExternalIdentity{}, ErrUnauthorized
		}
		return users.ExternalIdentity{}, fmt.Errorf("bind as %s: %w", entry.DN, err)
	}
	return ldapIdentity(settings, entry), nil
}

// Check binds as the service account and, when a username is given, finds
// that person's entry without needing their password — what the settings
// panel's test button shows.
func (l *LDAP) Check(ctx context.Context, settings LDAPSettings, username string) (users.ExternalIdentity, error) {
	conn, err := l.dial(ctx, settings)
	if err != nil {
		return users.ExternalIdentity{}, err
	}
	defer conn.Close()
	if err := l.serviceBind(conn, settings); err != nil {
		return users.ExternalIdentity{}, err
	}
	if strings.TrimSpace(username) == "" {
		return users.ExternalIdentity{}, nil
	}
	entry, err := l.search(conn, settings, strings.TrimSpace(username))
	if err != nil {
		return users.ExternalIdentity{}, err
	}
	return ldapIdentity(settings, entry), nil
}

func (l *LDAP) serviceBind(conn ldapConn, settings LDAPSettings) error {
	if strings.TrimSpace(settings.BindDN) == "" {
		return nil // an anonymous search, for directories that allow one
	}
	if err := conn.Bind(strings.TrimSpace(settings.BindDN), settings.BindPassword); err != nil {
		return fmt.Errorf("bind as the service account %s: %w", settings.BindDN, err)
	}
	return nil
}

func (l *LDAP) find(conn ldapConn, settings LDAPSettings, username string) (*ldap.Entry, error) {
	if err := l.serviceBind(conn, settings); err != nil {
		return nil, err
	}
	return l.search(conn, settings, username)
}

func (l *LDAP) search(conn ldapConn, settings LDAPSettings, username string) (*ldap.Entry, error) {
	filter := strings.ReplaceAll(settings.UserFilter, "{username}", ldap.EscapeFilter(username))
	attributes := []string{"dn", settings.UsernameAttr, settings.DisplayNameAttr, settings.EmailAttr, settings.GroupsAttr}
	if settings.SubjectAttr != "" {
		attributes = append(attributes, settings.SubjectAttr)
	} else {
		attributes = append(attributes, ldapSubjectFallbacks...)
	}
	result, err := conn.Search(ldap.NewSearchRequest(
		strings.TrimSpace(settings.BaseDN), ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		2, int(ldapTimeout/time.Second), false, filter, attributes, nil,
	))
	if err != nil && !ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
		return nil, fmt.Errorf("search %s: %w", settings.BaseDN, err)
	}
	if result == nil || len(result.Entries) == 0 {
		return nil, ErrUnauthorized
	}
	if len(result.Entries) > 1 {
		// Two people answer to this name; signing in as either would be a
		// guess. The filter is too loose.
		return nil, errors.New("the LDAP user filter matches more than one entry for this username")
	}
	return result.Entries[0], nil
}

func ldapIdentity(settings LDAPSettings, entry *ldap.Entry) users.ExternalIdentity {
	subject := ""
	if settings.SubjectAttr != "" {
		subject = entry.GetAttributeValue(settings.SubjectAttr)
	} else {
		for _, attr := range ldapSubjectFallbacks {
			if subject = entry.GetAttributeValue(attr); subject != "" {
				break
			}
		}
	}
	if subject == "" {
		subject = strings.ToLower(entry.DN)
	}
	return users.ExternalIdentity{
		Provider:    LDAPProviderID,
		Subject:     subject,
		Username:    entry.GetAttributeValue(settings.UsernameAttr),
		DisplayName: entry.GetAttributeValue(settings.DisplayNameAttr),
		Email:       entry.GetAttributeValue(settings.EmailAttr),
		Groups:      entry.GetAttributeValues(settings.GroupsAttr),
	}
}
