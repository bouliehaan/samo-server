// Package extauthtest is a fake OpenID Connect provider for tests: discovery,
// an authorize endpoint that signs whoever is "logged in" straight through, a
// token endpoint that checks the code, PKCE verifier, redirect URI and client
// credentials the way a real one does, JWKS and userinfo.
package extauthtest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	ClientID     = "samo"
	ClientSecret = "fake-client-secret"
)

// User is who the fake provider says is signed in.
type User struct {
	Subject  string
	Username string
	Name     string
	Email    string
	Groups   []string
}

type grant struct {
	user        User
	nonce       string
	challenge   string
	redirectURI string
}

type Provider struct {
	Server *httptest.Server
	key    *rsa.PrivateKey
	kid    string

	mu     sync.Mutex
	user   User
	grants map[string]grant
	// Tamper, when set, edits the ID token's claims before signing.
	Tamper func(claims map[string]any)
}

func NewProvider(t testing.TB) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{key: key, kid: "key-1", grants: make(map[string]grant)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("GET /jwks", p.jwks)
	mux.HandleFunc("GET /userinfo", p.userinfo)
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Server.Close)
	return p
}

// Issuer is the provider's issuer URL, trailing slash and all, the way
// authentik spells its own.
func (p *Provider) Issuer() string { return p.Server.URL + "/" }

// SignIn sets who the next authorize request signs in.
func (p *Provider) SignIn(user User) {
	p.mu.Lock()
	p.user = user
	p.mu.Unlock()
}

// RotateKey replaces the signing key under a new key id.
func (p *Provider) RotateKey(t testing.TB) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.key, p.kid = key, p.kid+"-next"
	p.mu.Unlock()
}

func (p *Provider) discovery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                 p.Issuer(),
		"authorization_endpoint": p.Server.URL + "/authorize",
		"token_endpoint":         p.Server.URL + "/token",
		"userinfo_endpoint":      p.Server.URL + "/userinfo",
		"jwks_uri":               p.Server.URL + "/jwks",
	})
}

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != ClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" ||
		!strings.Contains(" "+q.Get("scope")+" ", " openid ") {
		http.Error(w, "bad authorize request", http.StatusBadRequest)
		return
	}
	code := randomString()
	p.mu.Lock()
	p.grants[code] = grant{user: p.user, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri")}
	p.mu.Unlock()
	target, _ := url.Parse(q.Get("redirect_uri"))
	back := target.Query()
	back.Set("code", code)
	back.Set("state", q.Get("state"))
	target.RawQuery = back.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := r.BasicAuth()
	id, _ = url.QueryUnescape(id)
	secret, _ = url.QueryUnescape(secret)
	if !ok || id != ClientID || secret != ClientSecret {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]string{"error": "invalid_client"})
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	g, found := p.grants[r.PostForm.Get("code")]
	delete(p.grants, r.PostForm.Get("code"))
	key, kid, tamper := p.key, p.kid, p.Tamper
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !found || g.redirectURI != r.PostForm.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	now := time.Now()
	claims := map[string]any{
		"iss": p.Issuer(), "aud": ClientID, "sub": g.user.Subject,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "nonce": g.nonce,
		"preferred_username": g.user.Username, "name": g.user.Name,
	}
	if tamper != nil {
		tamper(claims)
	}
	access := "access-" + g.user.Subject
	writeJSON(w, map[string]any{"id_token": Sign(key, kid, claims), "access_token": access, "token_type": "Bearer"})
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	user := p.user
	p.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer access-"+user.Subject {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	// Email and groups only here, as some providers do: Exchange has to
	// read userinfo to see them.
	writeJSON(w, map[string]any{"sub": user.Subject, "email": user.Email, "groups": user.Groups})
}

func (p *Provider) jwks(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	key, kid := p.key, p.kid
	p.mu.Unlock()
	writeJSON(w, map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}})
}

// Sign makes an RS256 compact JWS of claims.
func Sign(key *rsa.PrivateKey, kid string, claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func randomString() string {
	buf := make([]byte, 16)
	rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}
