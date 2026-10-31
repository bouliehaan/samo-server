package extauth

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/extauth/extauthtest"
)

func fakeSettings(p *extauthtest.Provider) OIDCSettings {
	return Settings{OIDC: OIDCSettings{
		Enabled: true, Issuer: p.Issuer(), ClientID: extauthtest.ClientID, ClientSecret: extauthtest.ClientSecret,
	}}.withDefaults().OIDC
}

// signIn runs a whole flow against the fake provider and returns what
// Exchange made of it.
func signIn(t *testing.T, o *OIDC, settings OIDCSettings, mutate func(*Flow)) (string, error) {
	t.Helper()
	ctx := context.Background()
	flow, err := NewFlow("https://samo.example/auth/oidc/callback")
	if err != nil {
		t.Fatal(err)
	}
	authURL, err := o.AuthURL(ctx, settings, flow)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	back, err := url.Parse(response.Header.Get("Location"))
	if err != nil || back.Query().Get("state") != flow.State {
		t.Fatalf("provider did not send the state back: %q", response.Header.Get("Location"))
	}
	if mutate != nil {
		mutate(&flow)
	}
	identity, err := o.Exchange(ctx, settings, flow, back.Query().Get("code"))
	if err != nil {
		return "", err
	}
	return identity.Provider + "|" + identity.Subject + "|" + identity.Username + "|" + identity.Email + "|" + strings.Join(identity.Groups, ","), nil
}

func TestOIDCSignsInAndReadsUserinfo(t *testing.T) {
	p := extauthtest.NewProvider(t)
	p.SignIn(extauthtest.User{Subject: "abc123", Username: "jake", Name: "Jake", Email: "jake@example.org", Groups: []string{"samo-admins", "media"}})
	got, err := signIn(t, NewOIDC(), fakeSettings(p), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := ProviderID(p.Issuer()) + "|abc123|jake|jake@example.org|samo-admins,media"
	if got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}
}

// Each of these is an ID token a provider (or someone in between) might hand
// back that must not sign anyone in.
func TestOIDCRefusesTokensThatAreNotForThisSignIn(t *testing.T) {
	cases := map[string]struct {
		tamper func(map[string]any)
		flow   func(*Flow)
		want   string
	}{
		"another client's token":  {tamper: func(c map[string]any) { c["aud"] = "someone-else" }, want: "not for this client"},
		"another issuer's token":  {tamper: func(c map[string]any) { c["iss"] = "https://evil.example/" }, want: "issuer"},
		"an expired token":        {tamper: func(c map[string]any) { c["exp"] = 1000 }, want: "expired"},
		"a replayed token":        {tamper: func(c map[string]any) { c["nonce"] = "from-another-attempt" }, want: "not issued for this sign-in"},
		"the wrong PKCE verifier": {flow: func(f *Flow) { f.Verifier = "guessed" }, want: "refused"},
		"a token with no signer":  {tamper: func(c map[string]any) { c["sub"] = "" }, want: "did not say who"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := extauthtest.NewProvider(t)
			p.SignIn(extauthtest.User{Subject: "abc123", Username: "jake"})
			p.Tamper = tc.tamper
			_, err := signIn(t, NewOIDC(), fakeSettings(p), tc.flow)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestOIDCRefusesAWrongClientSecret(t *testing.T) {
	p := extauthtest.NewProvider(t)
	p.SignIn(extauthtest.User{Subject: "abc123", Username: "jake"})
	settings := fakeSettings(p)
	settings.ClientSecret = "wrong"
	if _, err := signIn(t, NewOIDC(), settings, nil); err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Fatalf("err = %v, want invalid_client", err)
	}
}

// A key rotation shows up as a token signed by a key id samo has not cached.
// It refetches the keys once instead of refusing everyone until the cache
// expires.
func TestOIDCFollowsAKeyRotation(t *testing.T) {
	p := extauthtest.NewProvider(t)
	p.SignIn(extauthtest.User{Subject: "abc123", Username: "jake"})
	o := NewOIDC()
	settings := fakeSettings(p)
	if _, err := signIn(t, o, settings, nil); err != nil {
		t.Fatal(err)
	}
	p.RotateKey(t)
	if _, err := signIn(t, o, settings, nil); err != nil {
		t.Fatalf("after rotation: %v", err)
	}
}

func TestOIDCDiscoveryMustBeTheIssuersOwn(t *testing.T) {
	p := extauthtest.NewProvider(t)
	settings := fakeSettings(p)
	settings.Issuer = p.Server.URL + "/somewhere-else"
	if err := NewOIDC().Check(context.Background(), settings); err == nil {
		t.Fatal("accepted a discovery document for another issuer")
	}
	// The trailing slash alone is forgiven.
	settings.Issuer = strings.TrimRight(p.Issuer(), "/")
	if err := NewOIDC().Check(context.Background(), settings); err != nil {
		t.Fatalf("issuer without its trailing slash: %v", err)
	}
}

func TestJWSRefusesNoneAndForgeries(t *testing.T) {
	p := extauthtest.NewProvider(t)
	settings := fakeSettings(p)
	provider, err := NewOIDC().provider(context.Background(), settings, false)
	if err != nil {
		t.Fatal(err)
	}
	// alg none: header {"alg":"none"}, any payload, no signature.
	if _, err := verifyJWS("eyJhbGciOiJub25lIn0.eyJzdWIiOiJ4In0.", provider.keys, ""); err == nil {
		t.Fatal("accepted alg none")
	}
	// HS256 with the RSA key's modulus as the secret is the classic
	// algorithm-confusion forgery; without a client secret it must fail, and
	// with one it must be the client secret that verifies, nothing else.
	if _, err := verifyJWS("eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.AAAA", provider.keys, ""); err == nil {
		t.Fatal("accepted HS256 with no client secret")
	}
}
