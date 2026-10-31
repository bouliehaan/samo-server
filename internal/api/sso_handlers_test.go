package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/extauth/extauthtest"
	"github.com/bouliehaan/samo-server/internal/users"
)

type ssoHarness struct {
	t        *testing.T
	handler  http.Handler
	idp      *extauthtest.Provider
	admin    string // admin bearer
	password string
}

func newSSOHarness(t *testing.T) *ssoHarness {
	t.Helper()
	handler, password := newPairingTestServer(t, true)
	h := &ssoHarness{t: t, handler: handler, idp: extauthtest.NewProvider(t), password: password}
	var login struct {
		Token string `json:"token"`
	}
	h.decode(h.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "admin", "password": password}, nil), http.StatusOK, &login)
	h.admin = login.Token
	return h
}

func (h *ssoHarness) do(method, target, bearer string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Host = "samo.example"
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func (h *ssoHarness) decode(rec *httptest.ResponseRecorder, status int, into any) {
	h.t.Helper()
	if rec.Code != status {
		h.t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if into != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *ssoHarness) configure(extra map[string]any) {
	h.t.Helper()
	settings := map[string]any{
		"publicUrl":     "https://samo.example",
		"autoProvision": true,
		"adminGroup":    "samo-admins",
		"oidc": map[string]any{
			"enabled": true, "label": "authentik", "issuer": h.idp.Issuer(),
			"clientId": extauthtest.ClientID, "clientSecret": extauthtest.ClientSecret,
		},
	}
	for key, value := range extra {
		settings[key] = value
	}
	var saved map[string]any
	h.decode(h.do(http.MethodPut, "/api/v1/auth/providers/config", h.admin, settings, nil), http.StatusOK, &saved)
	if strings.Contains(h.do(http.MethodGet, "/api/v1/auth/providers/config", h.admin, nil, nil).Body.String(), extauthtest.ClientSecret) {
		h.t.Fatal("the settings API handed the client secret back")
	}
	if saved["redirectUri"] != "https://samo.example/auth/oidc/callback" || saved["hasClientSecret"] != true {
		h.t.Fatalf("saved = %v", saved)
	}
}

// begin runs /auth/oidc/start and the provider's authorize redirect, and
// returns the callback URL the provider sent the browser to plus the binding
// cookie start set.
func (h *ssoHarness) begin() (string, *http.Cookie) {
	h.t.Helper()
	return h.beginAt("/auth/oidc/start")
}

func (h *ssoHarness) beginAt(start string) (string, *http.Cookie) {
	h.t.Helper()
	rec := h.do(http.MethodGet, start, "", nil, nil)
	if rec.Code != http.StatusFound {
		h.t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var binding *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == ssoCookie {
			binding = cookie
		}
	}
	if binding == nil || !binding.HttpOnly || !binding.Secure || binding.SameSite != http.SameSiteLaxMode {
		h.t.Fatalf("binding cookie = %+v", binding)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(rec.Header().Get("Location"))
	if err != nil {
		h.t.Fatal(err)
	}
	response.Body.Close()
	callback, err := url.Parse(response.Header.Get("Location"))
	if err != nil || callback.Host != "samo.example" || callback.Path != "/auth/oidc/callback" {
		h.t.Fatalf("provider sent the browser to %q", response.Header.Get("Location"))
	}
	return callback.RequestURI(), binding
}

// callback runs the redirect back and returns the hand-off code from the
// fragment, or the error the login page would show.
func (h *ssoHarness) callback(target string, cookie *http.Cookie) (code, failure string) {
	h.t.Helper()
	back := h.callbackURL(target, cookie)
	fragment, _ := url.ParseQuery(back.Fragment)
	return fragment.Get("sso"), back.Query().Get("sso_error")
}

// callbackURL is the /login address the callback sends the browser to. It
// always carries ?sso=done or ?sso_error=, which keep /login from sending
// the browser straight back to the provider.
func (h *ssoHarness) callbackURL(target string, cookie *http.Cookie) *url.URL {
	h.t.Helper()
	var cookies []*http.Cookie
	if cookie != nil {
		cookies = []*http.Cookie{cookie}
	}
	rec := h.do(http.MethodGet, target, "", nil, cookies)
	back, err := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || err != nil || back.Path != "/login" ||
		(back.Query().Get("sso") != "done" && !back.Query().Has("sso_error")) {
		h.t.Fatalf("callback: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	return back
}

func TestSSOSignsABrowserIn(t *testing.T) {
	h := newSSOHarness(t)

	var providers authProvidersResponse
	h.decode(h.do(http.MethodGet, "/api/v1/auth/providers", "", nil, nil), http.StatusOK, &providers)
	if providers.OIDC != nil {
		t.Fatal("offered SSO before it was configured")
	}
	h.configure(nil)
	h.decode(h.do(http.MethodGet, "/api/v1/auth/providers", "", nil, nil), http.StatusOK, &providers)
	if providers.OIDC == nil || providers.OIDC.Label != "authentik" {
		t.Fatalf("providers = %+v", providers)
	}

	h.idp.SignIn(extauthtest.User{Subject: "ak-1", Username: "jake", Name: "Jake", Email: "jake@example.org", Groups: []string{"samo-admins"}})
	target, binding := h.begin()
	code, failure := h.callback(target, binding)
	if code == "" {
		t.Fatalf("no hand-off: %s", failure)
	}

	var login struct {
		Token string     `json:"token"`
		User  users.User `json:"user"`
	}
	h.decode(h.do(http.MethodPost, "/api/v1/auth/oidc/complete", "", map[string]string{"code": code}, []*http.Cookie{binding}), http.StatusOK, &login)
	if login.User.Username != "jake" || login.User.Role != users.RoleAdmin || login.Token == "" {
		t.Fatalf("signed in as %+v", login.User)
	}
	var me users.User
	h.decode(h.do(http.MethodGet, "/api/v1/users/me", login.Token, nil, nil), http.StatusOK, &me)
	if me.ID != login.User.ID {
		t.Fatalf("the SSO token opened %s, not %s", me.ID, login.User.ID)
	}

	// The hand-off is single use.
	if rec := h.do(http.MethodPost, "/api/v1/auth/oidc/complete", "", map[string]string{"code": code}, []*http.Cookie{binding}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a hand-off redeemed twice: %d", rec.Code)
	}

	var identities struct {
		Items []users.Identity `json:"items"`
	}
	h.decode(h.do(http.MethodGet, "/api/v1/users/"+me.ID+"/identities", h.admin, nil, nil), http.StatusOK, &identities)
	if len(identities.Items) != 1 || identities.Items[0].Subject != "ak-1" {
		t.Fatalf("identities = %+v", identities.Items)
	}
}

// Login CSRF: someone runs a sign-in as themselves and sends the victim the
// callback, or the hand-off. Neither may finish in a browser that did not
// start that sign-in.
func TestSSOIsBoundToTheBrowserThatStartedIt(t *testing.T) {
	h := newSSOHarness(t)
	h.configure(nil)
	h.idp.SignIn(extauthtest.User{Subject: "ak-attacker", Username: "attacker"})

	target, _ := h.begin()
	if code, failure := h.callback(target, nil); code != "" || failure == "" {
		t.Fatalf("a callback without its binding cookie produced a hand-off")
	}

	target, binding := h.begin()
	code, _ := h.callback(target, binding)
	other := &http.Cookie{Name: ssoCookie, Value: strings.Repeat("0", 64)}
	if rec := h.do(http.MethodPost, "/api/v1/auth/oidc/complete", "", map[string]string{"code": code}, []*http.Cookie{other}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a hand-off redeemed by another browser: %d", rec.Code)
	}
}

func TestSSOWithoutProvisioningTellsTheLoginPageWhy(t *testing.T) {
	h := newSSOHarness(t)
	h.configure(map[string]any{"autoProvision": false})
	h.idp.SignIn(extauthtest.User{Subject: "ak-2", Username: "newcomer"})
	target, binding := h.begin()
	code, failure := h.callback(target, binding)
	if code != "" || !strings.Contains(failure, "ask an admin") {
		t.Fatalf("code %q failure %q", code, failure)
	}
}

func TestProviderSettingsAreAdminOnly(t *testing.T) {
	h := newSSOHarness(t)
	var created users.User
	h.decode(h.do(http.MethodPost, "/api/v1/users", h.admin, map[string]string{"username": "listener", "password": "listener-password"}, nil), http.StatusCreated, &created)
	var login struct {
		Token string `json:"token"`
	}
	h.decode(h.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "listener", "password": "listener-password"}, nil), http.StatusOK, &login)
	for _, req := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/auth/providers/config"},
		{http.MethodPut, "/api/v1/auth/providers/config"},
		{http.MethodPost, "/api/v1/auth/providers/test"},
		{http.MethodGet, "/api/v1/users/" + created.ID + "/identities"},
	} {
		if rec := h.do(req.method, req.target, login.Token, map[string]any{}, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a listener: %d", req.method, req.target, rec.Code)
		}
	}
}

// With LDAP on and the directory unreachable, the local admin made in setup
// still signs in: local accounts are always asked first.
func TestLocalLoginSurvivesABrokenDirectory(t *testing.T) {
	h := newSSOHarness(t)
	h.configure(map[string]any{"ldap": map[string]any{"enabled": true, "url": "ldap://127.0.0.1:1", "baseDn": "dc=example"}})
	if rec := h.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "admin", "password": h.password}, nil); rec.Code != http.StatusOK {
		t.Fatalf("local admin with the directory down: %d %s", rec.Code, rec.Body.String())
	}
	rec := h.do(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "someone", "password": "whatever"}, nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unknown user with the directory down: %d %s", rec.Code, rec.Body.String())
	}
}

// With a provider configured, /login is a redirect to it, like Nextcloud's
// and Immich's auto-launch, except where that would strand someone.
func TestLoginGoesStraightToTheProvider(t *testing.T) {
	h := newSSOHarness(t)
	if rec := h.do(http.MethodGet, "/login", "", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("/login without a provider: %d", rec.Code)
	}
	h.configure(nil)

	cases := map[string]string{
		"/login":                         "/auth/oidc/start",
		"/login?next=%2Fpair%23code%3DX": "/auth/oidc/start?next=%2Fpair%23code%3DX",
		"/login?next=%2F%2Fevil.example": "/auth/oidc/start",
		"/login?next=https%3A%2F%2Fevil": "/auth/oidc/start",
	}
	for target, want := range cases {
		rec := h.do(http.MethodGet, target, "", nil, nil)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("%s: %d %q, want %q", target, rec.Code, rec.Header().Get("Location"), want)
		}
	}
	// The back door when the provider is down, and the pages a sign-in
	// comes back to: a redirect from these would loop.
	for _, target := range []string{"/login?local=1", "/login?sso_error=nope", "/login?sso=done"} {
		if rec := h.do(http.MethodGet, target, "", nil, nil); rec.Code != http.StatusOK {
			t.Errorf("%s: %d %q, want the form", target, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// Where the browser was headed survives the provider without riding through
// it: the server keeps it and hands it back with the bearer, or with the
// error when the sign-in fails.
func TestSSOLandsWhereTheSignInBegan(t *testing.T) {
	h := newSSOHarness(t)
	h.configure(map[string]any{"requiredGroup": "samo-users"})

	h.idp.SignIn(extauthtest.User{Subject: "ak-1", Username: "jake", Groups: []string{"samo-users"}})
	target, binding := h.beginAt("/auth/oidc/start?next=" + url.QueryEscape("/pair#code=ABCD"))
	if strings.Contains(target, "ABCD") {
		t.Fatalf("the destination went through the provider: %s", target)
	}
	code, failure := h.callback(target, binding)
	if code == "" {
		t.Fatalf("no hand-off: %s", failure)
	}
	var login struct {
		Token string `json:"token"`
		Next  string `json:"next"`
	}
	h.decode(h.do(http.MethodPost, "/api/v1/auth/oidc/complete", "", map[string]string{"code": code}, []*http.Cookie{binding}), http.StatusOK, &login)
	if login.Token == "" || login.Next != "/pair#code=ABCD" {
		t.Fatalf("complete = %+v", login)
	}

	h.idp.SignIn(extauthtest.User{Subject: "ak-2", Username: "outsider"})
	target, binding = h.beginAt("/auth/oidc/start?next=%2Flisten%2F")
	back := h.callbackURL(target, binding)
	if !back.Query().Has("sso_error") || back.Query().Get("next") != "/listen/" {
		t.Fatalf("a refused sign-in came back to %s", back)
	}
}
