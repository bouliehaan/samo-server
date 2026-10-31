package extauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/users"
)

// OIDC is the authorization code flow with PKCE, as a confidential client:
//
//  1. AuthURL sends the browser to the provider with a state, a nonce and a
//     PKCE challenge the caller keeps.
//  2. The provider sends it back to the redirect URI with a code.
//  3. Exchange trades the code (and the PKCE verifier, and the client secret)
//     for an ID token at the token endpoint, verifies the token's signature
//     against the provider's published keys, checks issuer, audience, expiry
//     and nonce, and reads userinfo for anything the ID token left out.
//
// Discovery and keys are cached for an hour per issuer and refetched early
// when a token names a key samo has not seen, which is what a key rotation
// looks like from here.

const (
	discoveryTTL  = time.Hour
	clockSkew     = 2 * time.Minute
	maxOIDCBody   = 1 << 20
	oidcUserAgent = "samo-server"
)

type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

type providerCache struct {
	fetched time.Time
	forced  bool // fetched because a token named an unknown key
	meta    discovery
	keys    *jwks
}

// OIDC talks to whichever provider the settings name. One per server.
type OIDC struct {
	mu    sync.Mutex
	cache map[string]*providerCache // issuer|caCert → discovery and keys

	// now and client are swapped by tests.
	now    func() time.Time
	client func(caCert string) (*http.Client, error)
}

func NewOIDC() *OIDC {
	return &OIDC{cache: make(map[string]*providerCache), now: time.Now, client: httpClient}
}

func httpClient(caCert string) (*http.Client, error) {
	config, err := tlsConfig(caCert, false)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = config
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
		// The provider's endpoints are named by its discovery document; a
		// redirect away from one of them is not something to follow with a
		// client secret in hand.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// Flow is what one sign-in attempt keeps between AuthURL and Exchange.
type Flow struct {
	State       string
	Nonce       string
	Verifier    string
	RedirectURI string
}

// NewFlow makes the per-attempt secrets.
func NewFlow(redirectURI string) (Flow, error) {
	var flow Flow
	for _, field := range []*string{&flow.State, &flow.Nonce, &flow.Verifier} {
		value, err := randomString(32)
		if err != nil {
			return Flow{}, err
		}
		*field = value
	}
	flow.RedirectURI = redirectURI
	return flow, nil
}

func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// AuthURL is where to send the browser to begin a sign-in.
func (o *OIDC) AuthURL(ctx context.Context, settings OIDCSettings, flow Flow) (string, error) {
	provider, err := o.provider(ctx, settings, false)
	if err != nil {
		return "", err
	}
	challenge := sha256.Sum256([]byte(flow.Verifier))
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {settings.ClientID},
		"redirect_uri":          {flow.RedirectURI},
		"scope":                 {scopes(settings.Scopes)},
		"state":                 {flow.State},
		"nonce":                 {flow.Nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	endpoint, err := url.Parse(provider.meta.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("provider authorization endpoint: %w", err)
	}
	existing := endpoint.Query()
	for key, values := range query {
		existing[key] = values
	}
	endpoint.RawQuery = existing.Encode()
	return endpoint.String(), nil
}

// scopes always leads with openid, whatever the admin typed.
func scopes(extra string) string {
	out := []string{"openid"}
	for _, scope := range strings.Fields(strings.ReplaceAll(extra, ",", " ")) {
		if scope != "openid" {
			out = append(out, scope)
		}
	}
	return strings.Join(out, " ")
}

// ProviderID is the user_identities.provider an issuer's identities carry.
func ProviderID(issuer string) string {
	return "oidc:" + strings.TrimRight(strings.TrimSpace(issuer), "/")
}

// Exchange completes a sign-in: the code the provider sent back, for the flow
// that asked for it.
func (o *OIDC) Exchange(ctx context.Context, settings OIDCSettings, flow Flow, code string) (users.ExternalIdentity, error) {
	provider, err := o.provider(ctx, settings, false)
	if err != nil {
		return users.ExternalIdentity{}, err
	}
	client, err := o.client(settings.CACert)
	if err != nil {
		return users.ExternalIdentity{}, err
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {flow.RedirectURI},
		"code_verifier": {flow.Verifier},
		"client_id":     {settings.ClientID},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.meta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return users.ExternalIdentity{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", oidcUserAgent)
	if settings.ClientSecret != "" {
		// client_secret_basic, with the form-encoding RFC 6749 §2.3.1 asks
		// for and most providers (authentik included) expect.
		request.SetBasicAuth(url.QueryEscape(settings.ClientID), url.QueryEscape(settings.ClientSecret))
	}
	var tokens struct {
		IDToken          string `json:"id_token"`
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	status, err := doJSON(client, request, &tokens)
	if err != nil {
		return users.ExternalIdentity{}, fmt.Errorf("token endpoint: %w", err)
	}
	if status != http.StatusOK || tokens.IDToken == "" {
		reason := strings.TrimSpace(tokens.Error + " " + tokens.ErrorDescription)
		if reason == "" {
			reason = fmt.Sprintf("HTTP %d and no ID token", status)
		}
		return users.ExternalIdentity{}, fmt.Errorf("the provider refused the sign-in: %s", reason)
	}

	claims, err := o.verifyIDToken(ctx, settings, provider, tokens.IDToken, flow.Nonce)
	if err != nil {
		return users.ExternalIdentity{}, err
	}
	if tokens.AccessToken != "" && provider.meta.UserinfoEndpoint != "" {
		// Userinfo is best effort: a provider that puts everything in the ID
		// token, or none of it in userinfo, still signs people in.
		if info, err := o.userinfo(ctx, client, provider.meta.UserinfoEndpoint, tokens.AccessToken); err == nil {
			if sub, _ := info["sub"].(string); sub == claims.subject() {
				for key, value := range info {
					if _, ok := claims[key]; !ok {
						claims[key] = value
					}
				}
			}
		}
	}

	identity := users.ExternalIdentity{
		Provider:    ProviderID(provider.meta.Issuer),
		Subject:     claims.subject(),
		Username:    claims.str(settings.UsernameClaim),
		DisplayName: claims.str("name"),
		Email:       claims.str("email"),
		Groups:      claims.strings(settings.GroupsClaim),
	}
	if identity.Username == "" {
		identity.Username = claims.str("preferred_username")
	}
	if identity.Subject == "" {
		return users.ExternalIdentity{}, users.ErrInvalidIdentity
	}
	return identity, nil
}

func (o *OIDC) userinfo(ctx context.Context, client *http.Client, endpoint, accessToken string) (claimSet, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", oidcUserAgent)
	var info claimSet
	status, err := doJSON(client, request, &info)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("userinfo: HTTP %d", status)
	}
	return info, nil
}

// verifyIDToken checks the ID token is the provider's, for this client, for
// this attempt, and current.
func (o *OIDC) verifyIDToken(ctx context.Context, settings OIDCSettings, provider *providerCache, token, nonce string) (claimSet, error) {
	keys := provider.keys
	if needsRefresh(keys, token) {
		// A key id samo has not seen: the provider may have rotated. One
		// refetch, never one per request.
		refreshed, err := o.provider(ctx, settings, true)
		if err != nil {
			return nil, err
		}
		keys = refreshed.keys
	}
	payload, err := verifyJWS(token, keys, settings.ClientSecret)
	if err != nil {
		return nil, fmt.Errorf("ID token: %w", err)
	}
	var claims claimSet
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("ID token claims: %w", err)
	}
	now := o.now()
	if iss := claims.str("iss"); iss != provider.meta.Issuer {
		return nil, fmt.Errorf("ID token issuer %q is not %q", iss, provider.meta.Issuer)
	}
	audiences := claims.strings("aud")
	if !contains(audiences, settings.ClientID) {
		return nil, errors.New("ID token is not for this client")
	}
	if len(audiences) > 1 {
		if azp := claims.str("azp"); azp != "" && azp != settings.ClientID {
			return nil, errors.New("ID token was issued to another client")
		}
	}
	exp, ok := claims.number("exp")
	if !ok || now.After(time.Unix(int64(exp), 0).Add(clockSkew)) {
		return nil, errors.New("ID token has expired")
	}
	if iat, ok := claims.number("iat"); ok && time.Unix(int64(iat), 0).After(now.Add(clockSkew)) {
		return nil, errors.New("ID token is from the future; check the clocks")
	}
	if claims.str("nonce") != nonce {
		return nil, errors.New("ID token was not issued for this sign-in")
	}
	return claims, nil
}

// provider returns discovery and keys for the configured issuer, fetching
// them when missing, stale, or when force asks.
func (o *OIDC) provider(ctx context.Context, settings OIDCSettings, force bool) (*providerCache, error) {
	issuer := strings.TrimSpace(settings.Issuer)
	if issuer == "" {
		return nil, ErrDisabled
	}
	key := issuer + "|" + settings.CACert
	o.mu.Lock()
	cached := o.cache[key]
	o.mu.Unlock()
	if cached != nil && !force && o.now().Sub(cached.fetched) < discoveryTTL {
		return cached, nil
	}
	// One forced refetch per ten seconds: a run of tokens naming a key the
	// provider does not publish costs it one request, not one each.
	if cached != nil && force && cached.forced && o.now().Sub(cached.fetched) < 10*time.Second {
		return cached, nil
	}

	client, err := o.client(settings.CACert)
	if err != nil {
		return nil, err
	}
	discoveryURL := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", oidcUserAgent)
	var meta discovery
	status, err := doJSON(client, request, &meta)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery at %s: %w", discoveryURL, err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery at %s: HTTP %d", discoveryURL, status)
	}
	// The document must be the issuer's own (OIDC Discovery §4.3). Only a
	// trailing slash may differ: authentik's issuer ends in one that admins
	// often leave off.
	if strings.TrimRight(meta.Issuer, "/") != strings.TrimRight(issuer, "/") {
		return nil, fmt.Errorf("OIDC discovery at %s names issuer %q; set the issuer to exactly that", discoveryURL, meta.Issuer)
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" || meta.JWKSURI == "" {
		return nil, fmt.Errorf("OIDC discovery at %s is missing an endpoint", discoveryURL)
	}

	request, err = http.NewRequestWithContext(ctx, http.MethodGet, meta.JWKSURI, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", oidcUserAgent)
	var set jwks
	status, err = doJSON(client, request, &set)
	if err != nil {
		return nil, fmt.Errorf("OIDC keys at %s: %w", meta.JWKSURI, err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("OIDC keys at %s: HTTP %d", meta.JWKSURI, status)
	}

	fresh := &providerCache{fetched: o.now(), forced: force, meta: meta, keys: &set}
	o.mu.Lock()
	o.cache[key] = fresh
	o.mu.Unlock()
	return fresh, nil
}

// Check fetches discovery and keys afresh, so the settings panel can say
// whether samo can reach the provider before anyone tries to sign in.
func (o *OIDC) Check(ctx context.Context, settings OIDCSettings) error {
	_, err := o.provider(ctx, settings, true)
	return err
}

func doJSON(client *http.Client, request *http.Request, into any) (int, error) {
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOIDCBody))
	if err != nil {
		return response.StatusCode, err
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, into); err != nil && response.StatusCode == http.StatusOK {
			return response.StatusCode, fmt.Errorf("not JSON: %w", err)
		}
	}
	return response.StatusCode, nil
}

// claimSet is a decoded JWT or userinfo body.
type claimSet map[string]any

func (c claimSet) subject() string { return c.str("sub") }

func (c claimSet) str(name string) string {
	value, _ := c[name].(string)
	return strings.TrimSpace(value)
}

func (c claimSet) number(name string) (float64, bool) {
	value, ok := c[name].(float64)
	return value, ok
}

// strings reads a claim that may be one string or a list of them (aud, and
// groups on some providers).
func (c claimSet) strings(name string) []string {
	switch value := c[name].(type) {
	case string:
		if value = strings.TrimSpace(value); value != "" {
			return []string{value}
		}
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
