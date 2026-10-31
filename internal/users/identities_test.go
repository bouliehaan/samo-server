package users

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func newIdentityTestService(t *testing.T) (*Service, Principal) {
	t.Helper()
	ctx := context.Background()
	s := New(ServiceOptions{DB: storagetest.Open(t)})
	if err := s.Bootstrap(ctx, BootstrapInput{AdminUsername: "owner", AdminPassword: "owner-password"}); err != nil {
		t.Fatal(err)
	}
	owner, err := s.AuthenticateCredentials(ctx, "owner", "owner-password")
	if err != nil {
		t.Fatal(err)
	}
	return s, owner
}

var authentik = "oidc:https://auth.example/application/o/samo"

func TestResolveExternalProvisionsOnceAndThenFindsBySubject(t *testing.T) {
	ctx := context.Background()
	s, _ := newIdentityTestService(t)
	policy := ExternalPolicy{AutoProvision: true}
	identity := ExternalIdentity{Provider: authentik, Subject: "sub-1", Username: "Jake.Doe@Example.org", DisplayName: "Jake Doe"}

	first, err := s.ResolveExternal(ctx, identity, policy)
	if err != nil {
		t.Fatal(err)
	}
	if first.User.Username != "jake-doe-example-org" || first.User.DisplayName != "Jake Doe" || first.User.Role != RoleUser {
		t.Fatalf("provisioned %+v", first.User)
	}
	// A provisioned account has no password, so it cannot be opened with
	// one — not even an empty one.
	if _, err := s.AuthenticateCredentials(ctx, first.User.Username, ""); err == nil {
		t.Fatal("a provisioned account opened with an empty password")
	}

	// Renamed at the provider: still the same account, because the link is
	// the subject.
	identity.Username = "jdoe"
	again, err := s.ResolveExternal(ctx, identity, ExternalPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if again.User.ID != first.User.ID {
		t.Fatalf("second sign-in opened %s, first %s", again.User.ID, first.User.ID)
	}
	ids, err := s.ListIdentities(ctx, first.User.ID)
	if err != nil || len(ids) != 1 || ids[0].Username != "jdoe" {
		t.Fatalf("identities = %+v, %v", ids, err)
	}
}

func TestResolveExternalWithoutProvisioningRefusesStrangers(t *testing.T) {
	s, _ := newIdentityTestService(t)
	_, err := s.ResolveExternal(context.Background(), ExternalIdentity{Provider: authentik, Subject: "sub-1", Username: "stranger"}, ExternalPolicy{})
	if !errors.Is(err, ErrNotProvisioned) {
		t.Fatalf("err = %v", err)
	}
}

// An existing account is adopted by username only when the admin said so,
// never the server identity, and never one already linked to someone else at
// the same provider.
func TestResolveExternalLinksByUsernameOnlyWhenAllowed(t *testing.T) {
	ctx := context.Background()
	s, owner := newIdentityTestService(t)
	jake, err := s.Create(ctx, owner, CreateUserInput{Username: "jake", Password: "local-password"})
	if err != nil {
		t.Fatal(err)
	}
	identity := ExternalIdentity{Provider: authentik, Subject: "sub-jake", Username: "jake"}

	if _, err := s.ResolveExternal(ctx, identity, ExternalPolicy{AutoProvision: true}); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("without LinkByUsername: %v", err)
	}
	linked, err := s.ResolveExternal(ctx, identity, ExternalPolicy{LinkByUsername: true})
	if err != nil || linked.User.ID != jake.ID {
		t.Fatalf("linked %+v, %v; want %s", linked.User, err, jake.ID)
	}
	// The local password still works: linking adds a way in.
	if _, err := s.AuthenticateCredentials(ctx, "jake", "local-password"); err != nil {
		t.Fatal(err)
	}

	// A new person given the same name at the provider is not jake.
	impostor := ExternalIdentity{Provider: authentik, Subject: "sub-someone-else", Username: "jake"}
	if _, err := s.ResolveExternal(ctx, impostor, ExternalPolicy{LinkByUsername: true}); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("second identity adopted a linked account: %v", err)
	}
	server := ExternalIdentity{Provider: authentik, Subject: "sub-server", Username: "server"}
	if _, err := s.ResolveExternal(ctx, server, ExternalPolicy{LinkByUsername: true}); err == nil {
		t.Fatal("adopted the server identity")
	}
}

func TestResolveExternalAppliesGroups(t *testing.T) {
	ctx := context.Background()
	s, _ := newIdentityTestService(t)
	policy := ExternalPolicy{AutoProvision: true, AdminGroup: "samo-admins", RequiredGroup: "samo"}
	identity := ExternalIdentity{Provider: "ldap", Subject: "uid-1", Username: "jake", Groups: []string{"cn=media,ou=groups,dc=x"}}

	if _, err := s.ResolveExternal(ctx, identity, policy); !errors.Is(err, ErrNotInGroup) {
		t.Fatalf("outside the required group: %v", err)
	}
	// A directory names groups by DN; the admin types the plain name.
	identity.Groups = []string{"cn=samo,ou=groups,dc=x", "cn=samo-admins,ou=groups,dc=x"}
	admin, err := s.ResolveExternal(ctx, identity, policy)
	if err != nil || admin.User.Role != RoleAdmin {
		t.Fatalf("in the admin group: %+v, %v", admin.User, err)
	}
	identity.Groups = []string{"cn=samo,ou=groups,dc=x"}
	demoted, err := s.ResolveExternal(ctx, identity, policy)
	if err != nil || demoted.User.Role != RoleUser {
		t.Fatalf("left the admin group: %+v, %v", demoted.User, err)
	}
}

// The setup admin keeps the server reachable when the directory is wrong: a
// group sync cannot demote the last admin who has a password.
func TestResolveExternalNeverDemotesTheLastPasswordAdmin(t *testing.T) {
	ctx := context.Background()
	s, owner := newIdentityTestService(t)
	identity := ExternalIdentity{Provider: authentik, Subject: "sub-owner", Username: "owner"}
	got, err := s.ResolveExternal(ctx, identity, ExternalPolicy{LinkByUsername: true, AdminGroup: "samo-admins"})
	if err != nil {
		t.Fatal(err)
	}
	if got.User.ID != owner.User.ID || got.User.Role != RoleAdmin {
		t.Fatalf("the last password admin was demoted: %+v", got.User)
	}
}

func TestResolveExternalConcurrentFirstSignIns(t *testing.T) {
	ctx := context.Background()
	s, _ := newIdentityTestService(t)
	identity := ExternalIdentity{Provider: authentik, Subject: "sub-twice", Username: "twice"}
	var wg sync.WaitGroup
	ids := make([]string, 4)
	errs := make([]error, 4)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := s.ResolveExternal(ctx, identity, ExternalPolicy{AutoProvision: true})
			ids[i], errs[i] = p.User.ID, err
		}(i)
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("sign-in %d: %s, %v; first got %s", i, ids[i], errs[i], ids[0])
		}
	}
}

func TestUnlinkIdentity(t *testing.T) {
	ctx := context.Background()
	s, owner := newIdentityTestService(t)
	identity := ExternalIdentity{Provider: authentik, Subject: "sub-1", Username: "jake"}
	p, err := s.ResolveExternal(ctx, identity, ExternalPolicy{AutoProvision: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UnlinkIdentity(ctx, p, p.User.ID, authentik, "sub-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a non-admin unlinked: %v", err)
	}
	if err := s.UnlinkIdentity(ctx, owner, p.User.ID, authentik, "sub-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveExternal(ctx, identity, ExternalPolicy{}); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("after unlinking, the identity still opened the account: %v", err)
	}
}

func TestExternalUsername(t *testing.T) {
	for raw, want := range map[string]string{
		"Jake":                 "jake",
		"jake.doe@example.org": "jake-doe-example-org",
		"  Jake  Doe ":         "jake-doe",
		"__x__":                "__x__",
		"日本":                   "",
	} {
		if got := externalUsername(raw); got != want {
			t.Errorf("externalUsername(%q) = %q, want %q", raw, got, want)
		}
	}
}
