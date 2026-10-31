package api

// Single sign-on through an OpenID Connect provider (authentik, or any other),
// ending where a password sign-in ends: a bearer in the browser's
// localStorage, a samo-token like any other.
//
// samo has no cookie session and must not grow one (cors.go says why), so the
// browser is handed its bearer in two steps instead of a Set-Cookie:
//
//  1. GET /auth/oidc/start keeps the attempt's state, nonce and PKCE verifier
//     in memory and sends the browser to the provider. It also sets one
//     cookie, samo_sso, holding a random binding for this attempt. The cookie
//     authenticates nothing; it only proves that the browser finishing an
//     attempt is the one that began it, which is what stops someone sending a
//     victim a callback (or a hand-off) from their own sign-in and having the
//     victim's browser quietly signed in as them.
//  2. GET /auth/oidc/callback checks state and binding, has extauth trade the
//     code and verify the ID token, resolves the samo account, and sends the
//     browser to /login?sso=done#sso=<hand-off>. The hand-off is single use,
//     lives two minutes and rides in the fragment, so it never reaches a
//     server log.
//  3. The login page POSTs it to /api/v1/auth/oidc/complete — same-origin,
//     samo_sso attached — and gets the bearer a password sign-in returns,
//     plus the page the person was headed for when it all began.
//
// With a provider configured, GET /login does not draw the form at all: it
// sends the browser straight to step 1, the way Nextcloud and Immich do, so
// someone already signed in at the provider is simply in. ssoAutoLaunch says
// when it holds back.
//
// Apps that cannot run a browser flow of their own (Android, the desktop app,
// a TV) already sign in through /pair, and /pair signs in through /login, so
// SSO reaches them with no change to the app.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/extauth"
	"github.com/bouliehaan/samo-server/internal/log"
	"github.com/bouliehaan/samo-server/internal/users"
)

const (
	ssoCookie        = "samo_sso"
	ssoFlowLifetime  = 10 * time.Minute
	ssoHandoffExpiry = 2 * time.Minute
	ssoMaxPending    = 1024
	ssoCallbackPath  = "/auth/oidc/callback"
)

// next is where the browser was headed when the sign-in began: kept here
// rather than sent through the provider, since it can be /pair#code=…, a
// TV's pairing code.
type ssoFlow struct {
	flow    extauth.Flow
	binding string
	next    string
	expires time.Time
}

type ssoHandoff struct {
	userID  string
	binding string
	next    string
	expires time.Time
}

// ssoState is the attempts in flight. Memory only: a restart costs whoever is
// mid-sign-in one more click.
type ssoState struct {
	mu       sync.Mutex
	flows    map[string]*ssoFlow      // by state
	handoffs map[[32]byte]*ssoHandoff // by hash of the hand-off code
}

func newSSOState() *ssoState {
	return &ssoState{flows: make(map[string]*ssoFlow), handoffs: make(map[[32]byte]*ssoHandoff)}
}

func (p *ssoState) sweepLocked(now time.Time) {
	for key, flow := range p.flows {
		if !now.Before(flow.expires) {
			delete(p.flows, key)
		}
	}
	for key, handoff := range p.handoffs {
		if !now.Before(handoff.expires) {
			delete(p.handoffs, key)
		}
	}
}

func (p *ssoState) begin(flow extauth.Flow, binding, next string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked(now)
	if len(p.flows) >= ssoMaxPending {
		return false
	}
	p.flows[flow.State] = &ssoFlow{flow: flow, binding: binding, next: next, expires: now.Add(ssoFlowLifetime)}
	return true
}

// finish takes the attempt a callback names; it can be taken once.
func (p *ssoState) finish(state string, now time.Time) (*ssoFlow, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked(now)
	flow, ok := p.flows[state]
	if ok {
		delete(p.flows, state)
	}
	return flow, ok
}

func (p *ssoState) handOff(userID, binding, next string, now time.Time) (string, error) {
	code, err := newSSOSecret()
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked(now)
	if len(p.handoffs) >= ssoMaxPending {
		return "", errPairingBusy
	}
	p.handoffs[sha256.Sum256([]byte(code))] = &ssoHandoff{userID: userID, binding: binding, next: next, expires: now.Add(ssoHandoffExpiry)}
	return code, nil
}

func (p *ssoState) redeem(code string, now time.Time) (*ssoHandoff, bool) {
	key := sha256.Sum256([]byte(code))
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked(now)
	handoff, ok := p.handoffs[key]
	if ok {
		delete(p.handoffs, key)
	}
	return handoff, ok
}

// newSSOSecret is 256 random bits, hex: a binding or a hand-off code.
func newSSOSecret() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func sameSecret(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ssoBaseURL is where browsers reach this server: the configured public URL,
// or failing that the address this request arrived on.
func ssoBaseURL(settings extauth.Settings, r *http.Request) string {
	if base := strings.TrimRight(strings.TrimSpace(settings.PublicURL), "/"); base != "" {
		return base
	}
	return strings.TrimRight(publicURL(r, ""), "/")
}

// authProvidersResponse is what the login page needs to draw itself.
type authProvidersResponse struct {
	Password bool             `json:"password"`
	LDAP     bool             `json:"ldap"`
	OIDC     *authOIDCSummary `json:"oidc,omitempty"`
}

type authOIDCSummary struct {
	Label    string `json:"label"`
	StartURL string `json:"startUrl"`
}

// getAuthProviders says which sign-in methods the login page should offer.
// Public: it says only whether each is on, and the button's label.
func (s *Server) getAuthProviders(w http.ResponseWriter, r *http.Request) {
	response := authProvidersResponse{Password: true}
	if s.extAuth != nil {
		if settings, err := s.extAuth.Store.Settings(r.Context()); err == nil {
			response.LDAP = settings.LDAPReady()
			if settings.OIDCReady() {
				response.OIDC = &authOIDCSummary{Label: settings.OIDC.Label, StartURL: "/auth/oidc/start"}
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// ssoNext is a sign-in's destination if it is a path on this server, and
// empty otherwise. The login page checks it again before going there.
func ssoNext(raw string) string {
	if len(raw) > 2048 || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") {
		return ""
	}
	for _, c := range raw {
		if c < 0x20 || c == 0x7f {
			return ""
		}
	}
	return raw
}

// ssoAutoLaunch is where GET /login sends the browser instead of drawing the
// form, or "" to draw it. It holds back for ?local=1, the way in when the
// provider is down, and for a page coming back from a sign-in (?sso=done,
// ?sso_error=), or a failed sign-in would start the next one forever.
func (s *Server) ssoAutoLaunch(r *http.Request) string {
	query := r.URL.Query()
	if s.extAuth == nil || query.Has("local") || query.Has("sso") || query.Has("sso_error") {
		return ""
	}
	settings, err := s.extAuth.Store.Settings(r.Context())
	if err != nil || !settings.OIDCReady() {
		return ""
	}
	if next := ssoNext(query.Get("next")); next != "" {
		return "/auth/oidc/start?next=" + url.QueryEscape(next)
	}
	return "/auth/oidc/start"
}

// ssoFail sends the browser back to the login page with a reason it can show,
// and the form: the query, not the fragment, so /login sees it and does not
// send the browser straight back to the provider.
func ssoFail(w http.ResponseWriter, r *http.Request, message, next string) {
	target := "/login?sso_error=" + url.QueryEscape(message)
	if next != "" {
		target += "&next=" + url.QueryEscape(next)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) startOIDC(w http.ResponseWriter, r *http.Request) {
	next := ssoNext(r.URL.Query().Get("next"))
	if s.extAuth == nil {
		ssoFail(w, r, "single sign-on is not configured", next)
		return
	}
	settings, err := s.extAuth.Store.Settings(r.Context())
	if err != nil || !settings.OIDCReady() {
		ssoFail(w, r, "single sign-on is not configured", next)
		return
	}
	flow, err := extauth.NewFlow(ssoBaseURL(settings, r) + ssoCallbackPath)
	if err != nil {
		ssoFail(w, r, "could not start sign-in", next)
		return
	}
	authURL, err := s.extAuth.OIDC.AuthURL(r.Context(), settings.OIDC, flow)
	if err != nil {
		log.Warnf("sso: %v", err)
		ssoFail(w, r, "samo could not reach "+settings.OIDC.Label, next)
		return
	}
	binding, err := newSSOSecret()
	if err != nil {
		ssoFail(w, r, "could not start sign-in", next)
		return
	}
	if !s.ssoState.begin(flow, binding, next, time.Now()) {
		ssoFail(w, r, "too many sign-ins in progress; try again in a minute", next)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:  ssoCookie,
		Value: binding,
		Path:  "/",
		// Lax, not Strict: the provider's redirect back is a cross-site
		// top-level navigation, and Strict would leave the cookie behind.
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
		Secure:   strings.HasPrefix(flow.RedirectURI, "https://"),
		MaxAge:   int(ssoFlowLifetime / time.Second),
	})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, authURL, http.StatusFound)
}

func ssoBinding(r *http.Request) string {
	cookie, err := r.Cookie(ssoCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) finishOIDC(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.extAuth == nil {
		ssoFail(w, r, "single sign-on is not configured", "")
		return
	}
	query := r.URL.Query()
	pending, ok := s.ssoState.finish(query.Get("state"), time.Now())
	if !ok || !sameSecret(pending.binding, ssoBinding(r)) {
		ssoFail(w, r, "this sign-in expired or was started in another browser; try again", "")
		return
	}
	if reason := query.Get("error"); reason != "" {
		if description := query.Get("error_description"); description != "" {
			reason = description
		}
		ssoFail(w, r, reason, pending.next)
		return
	}
	settings, err := s.extAuth.Store.Settings(r.Context())
	if err != nil || !settings.OIDCReady() {
		ssoFail(w, r, "single sign-on is not configured", pending.next)
		return
	}
	identity, err := s.extAuth.OIDC.Exchange(r.Context(), settings.OIDC, pending.flow, query.Get("code"))
	if err != nil {
		log.Warnf("sso: %v", err)
		ssoFail(w, r, "sign-in with "+settings.OIDC.Label+" failed; the server log says why", pending.next)
		return
	}
	principal, err := s.users.ResolveExternal(r.Context(), identity, settings.Policy())
	if err != nil {
		log.Infof("sso: %s identity %q refused: %v", identity.Provider, identity.Username, err)
		ssoFail(w, r, externalRefusal(err), pending.next)
		return
	}
	code, err := s.ssoState.handOff(principal.User.ID, pending.binding, pending.next, time.Now())
	if err != nil {
		ssoFail(w, r, "too many sign-ins in progress; try again in a minute", pending.next)
		return
	}
	http.Redirect(w, r, "/login?sso=done#sso="+url.QueryEscape(code), http.StatusFound)
}

// completeOIDC trades a hand-off for a bearer, in the shape /auth/login
// returns, plus where the sign-in was headed. The hand-off code and the
// samo_sso cookie together are the credential.
func (s *Server) completeOIDC(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Code string `json:"code"`
	}
	if !readJSONBody(w, r, &input) {
		return
	}
	handoff, ok := s.ssoState.redeem(strings.TrimSpace(input.Code), time.Now())
	if !ok || !sameSecret(handoff.binding, ssoBinding(r)) {
		writeError(w, http.StatusUnauthorized, "this sign-in expired or was started in another browser; try again")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	user, err := s.users.Get(r.Context(), handoff.userID)
	if err != nil {
		writeUserError(w, err)
		return
	}
	response, err := s.users.IssueLogin(r.Context(), users.Principal{User: user}, "sso")
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		loginResponse
		Next string `json:"next,omitempty"`
	}{loginResponse{LoginResponse: response, ServerID: s.serverIdentity(r.Context())}, handoff.next})
}

// externalRefusal is what to tell someone the provider vouched for but samo
// would not let in.
func externalRefusal(err error) string {
	switch {
	case errors.Is(err, users.ErrNotProvisioned), errors.Is(err, users.ErrNotInGroup), errors.Is(err, users.ErrIdentityConflict):
		return err.Error()
	case errors.Is(err, users.ErrInvalidUsername):
		return "the identity provider sent no username samo can use"
	case errors.Is(err, users.ErrInvalidIdentity):
		return err.Error()
	}
	return "sign-in failed; the server log says why"
}

// ldapLogin is the password form's second chance: a username and password
// the local accounts refused, asked of the directory. Local accounts are
// asked first, always, so the admin made in setup still signs in when the
// directory is down or misconfigured.
func (s *Server) ldapLogin(ctx context.Context, input users.LoginInput) (users.LoginResponse, error) {
	if s.extAuth == nil {
		return users.LoginResponse{}, users.ErrUnauthorized
	}
	settings, err := s.extAuth.Store.Settings(ctx)
	if err != nil || !settings.LDAPReady() {
		return users.LoginResponse{}, users.ErrUnauthorized
	}
	identity, err := s.extAuth.LDAP.Authenticate(ctx, settings.LDAP, input.Username, input.Password)
	if errors.Is(err, extauth.ErrUnauthorized) {
		return users.LoginResponse{}, users.ErrUnauthorized
	}
	if err != nil {
		log.Warnf("ldap: %v", err)
		return users.LoginResponse{}, errDirectoryUnavailable
	}
	principal, err := s.users.ResolveExternal(ctx, identity, settings.Policy())
	if err != nil {
		log.Infof("ldap: identity %q refused: %v", identity.Username, err)
		return users.LoginResponse{}, err
	}
	return s.users.IssueLogin(ctx, principal, "ldap")
}

var errDirectoryUnavailable = errors.New("the sign-in directory could not be reached; try again, or sign in with a local account")

// Admin: the settings panel.

type authProviderConfigResponse struct {
	extauth.Public
	// RedirectURI is what to register at the provider, worked out the same
	// way a sign-in will.
	RedirectURI string `json:"redirectUri"`
}

func (s *Server) getAuthProviderConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.extAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "sign-in providers need the database")
		return
	}
	public, err := s.extAuth.Store.Public(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, authProviderConfigResponse{Public: public, RedirectURI: ssoBaseURL(public.Settings, r) + ssoCallbackPath})
}

func (s *Server) updateAuthProviderConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.extAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "sign-in providers need the database")
		return
	}
	var input extauth.Settings
	if !readJSONBody(w, r, &input) {
		return
	}
	public, err := s.extAuth.Store.Save(r.Context(), input)
	if errors.Is(err, extauth.ErrInvalidConfig) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, authProviderConfigResponse{Public: public, RedirectURI: ssoBaseURL(public.Settings, r) + ssoCallbackPath})
}

// testAuthProvider tries the saved settings against the provider without
// anyone signing in: OIDC discovery and keys, or an LDAP service bind and,
// given a username, that person's entry.
func (s *Server) testAuthProvider(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.extAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "sign-in providers need the database")
		return
	}
	var input struct {
		Provider string `json:"provider"`
		Username string `json:"username"`
	}
	if !readJSONBody(w, r, &input) {
		return
	}
	settings, err := s.extAuth.Store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch input.Provider {
	case "oidc":
		if err := s.extAuth.OIDC.Check(r.Context(), settings.OIDC); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "samo reached " + settings.OIDC.Label + " and read its signing keys."})
	case "ldap":
		identity, err := s.extAuth.LDAP.Check(r.Context(), settings.LDAP, input.Username)
		if errors.Is(err, extauth.ErrUnauthorized) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "the service account bound, but the user filter found no entry for " + input.Username})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		if strings.TrimSpace(input.Username) == "" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "the service account bound."})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "found " + identity.Username, "identity": map[string]any{
			"username": identity.Username, "displayName": identity.DisplayName, "email": identity.Email,
			"groups": identity.Groups, "subject": identity.Subject,
		}})
	default:
		writeError(w, http.StatusBadRequest, "provider must be oidc or ldap")
	}
}

// Admin: the identities linked to an account.

func (s *Server) listUserIdentities(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	items, err := s.users.ListIdentities(r.Context(), r.PathValue("id"))
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) unlinkUserIdentity(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	if err := s.users.UnlinkIdentity(r.Context(), principal, r.PathValue("id"), query.Get("provider"), query.Get("subject")); err != nil {
		writeUserError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
