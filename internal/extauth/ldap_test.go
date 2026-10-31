package extauth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

// fakeDirectory is a directory with entries and their passwords.
type fakeDirectory struct {
	entries   []*ldap.Entry
	passwords map[string]string // DN → password
	filters   []string
	binds     []string
}

type fakeConn struct{ dir *fakeDirectory }

func (c fakeConn) Bind(dn, password string) error {
	c.dir.binds = append(c.dir.binds, dn)
	if want, ok := c.dir.passwords[dn]; ok && want == password {
		return nil
	}
	return ldap.NewError(ldap.LDAPResultInvalidCredentials, errors.New("invalid credentials"))
}

func (c fakeConn) Search(request *ldap.SearchRequest) (*ldap.SearchResult, error) {
	c.dir.filters = append(c.dir.filters, request.Filter)
	// Enough of a filter engine for these tests: the escaped username is
	// what the entry's cn must equal.
	var matches []*ldap.Entry
	for _, entry := range c.dir.entries {
		if strings.Contains(request.Filter, "(cn="+ldap.EscapeFilter(entry.GetAttributeValue("cn"))+")") {
			matches = append(matches, entry)
		}
	}
	return &ldap.SearchResult{Entries: matches}, nil
}

func (c fakeConn) Close() error { return nil }

func authentikDirectory() *fakeDirectory {
	const base = "dc=ldap,dc=goauthentik,dc=io"
	jake := ldap.NewEntry("cn=jake,ou=users,"+base, map[string][]string{
		"cn": {"jake"}, "displayName": {"Jake"}, "mail": {"jake@example.org"}, "uid": {"6a1f…unique"},
		"memberOf": {"cn=samo-admins,ou=groups," + base, "cn=media,ou=groups," + base},
	})
	return &fakeDirectory{
		entries: []*ldap.Entry{jake},
		passwords: map[string]string{
			"cn=samo-search,ou=users," + base: "service-password",
			"cn=jake,ou=users," + base:        "jakes-password",
		},
	}
}

func testLDAP(dir *fakeDirectory) (*LDAP, LDAPSettings) {
	l := &LDAP{dial: func(context.Context, LDAPSettings) (ldapConn, error) { return fakeConn{dir}, nil }}
	settings := Settings{LDAP: LDAPSettings{
		Enabled: true, URL: "ldap://outpost:3389", BaseDN: "dc=ldap,dc=goauthentik,dc=io",
		BindDN: "cn=samo-search,ou=users,dc=ldap,dc=goauthentik,dc=io", BindPassword: "service-password",
	}}.withDefaults().LDAP
	return l, settings
}

func TestLDAPSearchesThenBindsAsThePerson(t *testing.T) {
	dir := authentikDirectory()
	l, settings := testLDAP(dir)
	identity, err := l.Authenticate(context.Background(), settings, "jake", "jakes-password")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Provider != LDAPProviderID || identity.Subject != "6a1f…unique" || identity.Username != "jake" ||
		identity.Email != "jake@example.org" || len(identity.Groups) != 2 {
		t.Fatalf("identity = %+v", identity)
	}
	if len(dir.binds) != 2 || dir.binds[0] != settings.BindDN || dir.binds[1] != "cn=jake,ou=users,dc=ldap,dc=goauthentik,dc=io" {
		t.Fatalf("binds = %v; want the service account, then the person", dir.binds)
	}
}

func TestLDAPRefusesAWrongPasswordAndAnUnknownName(t *testing.T) {
	l, settings := testLDAP(authentikDirectory())
	if _, err := l.Authenticate(context.Background(), settings, "jake", "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := l.Authenticate(context.Background(), settings, "nobody", "whatever"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown user: %v", err)
	}
}

// An empty password is an anonymous bind, which directories routinely accept.
// It must be refused before the directory is even asked.
func TestLDAPNeverBindsWithAnEmptyPassword(t *testing.T) {
	dir := authentikDirectory()
	dir.passwords["cn=jake,ou=users,dc=ldap,dc=goauthentik,dc=io"] = ""
	l, settings := testLDAP(dir)
	if _, err := l.Authenticate(context.Background(), settings, "jake", ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("empty password: %v", err)
	}
	if len(dir.binds) != 0 {
		t.Fatalf("asked the directory with an empty password: %v", dir.binds)
	}
}

// A username is data in a filter, never filter syntax: "*" must not match
// everyone, and a ")(" must not open a clause of its own.
func TestLDAPEscapesTheUsername(t *testing.T) {
	dir := authentikDirectory()
	l, settings := testLDAP(dir)
	for _, name := range []string{"*", "jake)(cn=*", `j\ake`} {
		if _, err := l.Authenticate(context.Background(), settings, name, "jakes-password"); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("%q: %v", name, err)
		}
	}
	for _, filter := range dir.filters {
		if strings.Contains(filter, "(cn=*)") || strings.Contains(filter, "jake)(cn=") {
			t.Fatalf("username reached the filter unescaped: %s", filter)
		}
	}
}

// authentik answers a bind by someone outside the application's group with
// result 50. That is a refusal, not the directory being down.
func TestLDAPTreatsInsufficientAccessAsARefusal(t *testing.T) {
	dir := authentikDirectory()
	l, settings := testLDAP(dir)
	l.dial = func(context.Context, LDAPSettings) (ldapConn, error) { return refusingConn{fakeConn{dir}}, nil }
	if _, err := l.Authenticate(context.Background(), settings, "jake", "jakes-password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("result 50: %v", err)
	}
}

type refusingConn struct{ fakeConn }

func (c refusingConn) Bind(dn, password string) error {
	if strings.HasPrefix(dn, "cn=samo-search,") {
		return c.fakeConn.Bind(dn, password)
	}
	return ldap.NewError(ldap.LDAPResultInsufficientAccessRights, errors.New("insufficient access"))
}

func TestLDAPRefusesAnAmbiguousFilter(t *testing.T) {
	dir := authentikDirectory()
	dir.entries = append(dir.entries, ldap.NewEntry("cn=jake,ou=other,dc=ldap,dc=goauthentik,dc=io", map[string][]string{"cn": {"jake"}}))
	l, settings := testLDAP(dir)
	if _, err := l.Authenticate(context.Background(), settings, "jake", "jakes-password"); err == nil || errors.Is(err, ErrUnauthorized) {
		t.Fatalf("two entries for one name: %v", err)
	}
}

func TestLDAPSubjectFallsBackToTheDN(t *testing.T) {
	entry := ldap.NewEntry("CN=Jake,OU=Users,DC=example", map[string][]string{"cn": {"jake"}})
	if got := ldapIdentity(Settings{}.withDefaults().LDAP, entry).Subject; got != "cn=jake,ou=users,dc=example" {
		t.Fatalf("subject = %q", got)
	}
}

func TestSettingsValidate(t *testing.T) {
	bad := []Settings{
		{PublicURL: "samo.example.com"},
		{OIDC: OIDCSettings{Enabled: true, Issuer: "not a url", ClientID: "x"}},
		{OIDC: OIDCSettings{Enabled: true, Issuer: "https://auth.example/", ClientID: ""}},
		{OIDC: OIDCSettings{Enabled: true, Issuer: "https://auth.example/", ClientID: "x", CACert: "not pem"}},
		{LDAP: LDAPSettings{Enabled: true, URL: "http://outpost", BaseDN: "dc=x"}},
		{LDAP: LDAPSettings{Enabled: true, URL: "ldap://outpost", BaseDN: ""}},
		{LDAP: LDAPSettings{Enabled: true, URL: "ldap://outpost", BaseDN: "dc=x", UserFilter: "(cn=jake)"}},
	}
	for i, settings := range bad {
		if err := settings.Validate(); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("case %d: %v", i, err)
		}
	}
	good := Settings{
		PublicURL: "https://samo.example.com",
		OIDC:      OIDCSettings{Enabled: true, Issuer: "https://auth.example/application/o/samo/", ClientID: "x"},
		LDAP:      LDAPSettings{Enabled: true, URL: "ldaps://outpost:6636", BaseDN: "dc=ldap,dc=goauthentik,dc=io"},
	}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
}
