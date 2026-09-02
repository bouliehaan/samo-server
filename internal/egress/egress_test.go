package egress

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDisabledRouterIsInert(t *testing.T) {
	router, err := NewRouter(Options{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	if router.Enabled() {
		t.Fatal("a blank proxy URL produced an enabled router")
	}
	if router.Routes("cdn-images.dzcdn.net") {
		t.Error("a disabled router routed a host")
	}

	base := &http.Client{}
	if got := Client(router, base); got != base {
		t.Error("a disabled router did not return the base client unchanged")
	}
}

func TestRouterRoutesOnlyListedHosts(t *testing.T) {
	router, err := NewRouter(Options{
		ProxyURL: "http://samo:token@192.168.1.12:6768",
		Hosts:    "cdn-images.dzcdn.net, .example.org",
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	for _, host := range []string{
		"cdn-images.dzcdn.net",
		"CDN-IMAGES.DZCDN.NET",
		"cdn-images.dzcdn.net.",
		"a.example.org",
		"deep.nested.example.org",
	} {
		if !router.Routes(host) {
			t.Errorf("Routes(%q) = false, want true", host)
		}
	}

	// Everything not named stays on the VPN. api.deezer.com is the one that
	// matters most: the lookup carries artist names out of the library, and it
	// works fine over the VPN, so it must not be routed.
	for _, host := range []string{
		"api.deezer.com",
		"ws.audioscrobbler.com",
		"dzcdn.net",
		"example.org.evil.com",
		"notexample.org",
		"cdn-images.dzcdn.net.evil.com",
		"",
	} {
		if router.Routes(host) {
			t.Errorf("Routes(%q) = true, want false", host)
		}
	}
}

func TestDefaultHostsUsedWhenListOmitted(t *testing.T) {
	router, err := NewRouter(Options{ProxyURL: "http://samo:token@192.168.1.12:6768"})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	for _, host := range DefaultHosts {
		if !router.Routes(host) {
			t.Errorf("default list does not route %q", host)
		}
	}
	if router.Routes("api.deezer.com") {
		t.Error("the default list routes api.deezer.com; only the CDN should leave the VPN")
	}
}

func TestNewRouterRejectsUnsafeConfiguration(t *testing.T) {
	for name, options := range map[string]Options{
		"bad scheme":  {ProxyURL: "ftp://192.168.1.12:6768"},
		"no host":     {ProxyURL: "http://"},
		"wildcard":    {ProxyURL: "http://192.168.1.12:6768", Hosts: "*"},
		"bare dot":    {ProxyURL: "http://192.168.1.12:6768", Hosts: "."},
		"url in list": {ProxyURL: "http://192.168.1.12:6768", Hosts: "https://cdn.example"},
		"empty list":  {ProxyURL: "http://192.168.1.12:6768", Hosts: ","},
	} {
		if _, err := NewRouter(options); err == nil {
			t.Errorf("%s: NewRouter succeeded, want error", name)
		}
	}
}

// TestProxyForPicksProxyOnlyForListedHosts exercises the hook http.Transport
// actually calls, since that is where a mistake would silently send everything
// off the VPN rather than failing loudly.
func TestProxyForPicksProxyOnlyForListedHosts(t *testing.T) {
	router, err := NewRouter(Options{
		ProxyURL: "http://samo:token@192.168.1.12:6768",
		Hosts:    "cdn-images.dzcdn.net",
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	routed, _ := url.Parse("https://cdn-images.dzcdn.net/images/artist/abc/1000x1000.jpg")
	proxy, err := router.proxyFor(&http.Request{URL: routed})
	if err != nil {
		t.Fatalf("proxyFor: %v", err)
	}
	if proxy == nil || proxy.Host != "192.168.1.12:6768" {
		t.Fatalf("listed host: proxy = %v, want the configured proxy", proxy)
	}
	if password, _ := proxy.User.Password(); password != "token" {
		t.Error("the credential was dropped from the proxy URL")
	}

	direct, _ := url.Parse("https://api.deezer.com/search/artist?q=x")
	proxy, err = router.proxyFor(&http.Request{URL: direct})
	if err != nil {
		t.Fatalf("proxyFor: %v", err)
	}
	if proxy != nil {
		t.Errorf("unlisted host: proxy = %v, want nil", proxy)
	}

	if proxy, err := router.proxyFor(nil); proxy != nil || err != nil {
		t.Error("a nil request should route directly rather than panic")
	}
}

func TestClientPreservesBaseTimeout(t *testing.T) {
	router, err := NewRouter(Options{ProxyURL: "http://samo:token@192.168.1.12:6768"})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	base := &http.Client{Timeout: 42}
	client := Client(router, base)
	if client == base {
		t.Fatal("an enabled router returned the base client unchanged")
	}
	if client.Timeout != 42 {
		t.Errorf("Timeout = %v, want the base client's 42", client.Timeout)
	}
	if base.Transport != nil {
		t.Error("the base client's transport was mutated")
	}
}

// --- fallback behaviour ----------------------------------------------------
//
// "Direct" on the samo box means "through the VPN", so falling back is the more
// private path, not the less. These tests pin the behaviour that matters: a
// missing samo-proxy must degrade to exactly what happened before this package
// existed, never to a failed request.

// deadProxyRouter points at a port nothing is listening on, which is what an
// absent or stopped samo-proxy actually looks like from here.
func deadProxyRouter(t *testing.T, host string, logger func(string, ...any)) *Router {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close() // nothing listens here now

	router, err := NewRouter(Options{
		ProxyURL: "http://samo:token@" + addr,
		Hosts:    host,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

func TestFallsBackToDirectWhenProxyIsAbsent(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("direct-bytes"))
	}))
	defer origin.Close()
	originHost := strings.TrimPrefix(origin.URL, "http://")
	host, _, _ := net.SplitHostPort(originHost)

	var logged []string
	router := deadProxyRouter(t, host, func(format string, args ...any) {
		logged = append(logged, format)
	})

	resp, err := Client(router, nil).Get(origin.URL)
	if err != nil {
		t.Fatalf("request failed instead of falling back: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "direct-bytes" {
		t.Fatalf("body = %q, want the direct response", body)
	}
	if len(logged) != 1 {
		t.Errorf("logged %d lines, want exactly 1 for the outage", len(logged))
	}
}

func TestProxyOutageIsAnnouncedOnceThenCoolsDown(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer origin.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(origin.URL, "http://"))

	var lines int
	router := deadProxyRouter(t, host, func(string, ...any) { lines++ })
	client := Client(router, nil)

	for i := 0; i < 5; i++ {
		resp, err := client.Get(origin.URL)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
	}
	if lines != 1 {
		t.Errorf("logged %d times across 5 requests, want 1", lines)
	}

	// After the first failure the proxy is resting, so later requests must not
	// pay its dial timeout again.
	transport := client.Transport.(*fallbackTransport)
	if !transport.proxyResting() {
		t.Error("proxy was not put into a cooldown after failing")
	}
}

func TestUnroutedHostSkipsTheProxyEntirely(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer origin.Close()

	// The router lists a host this request does not use, so the dead proxy must
	// never be consulted and nothing should be logged.
	var lines int
	router := deadProxyRouter(t, "cdn-images.dzcdn.net", func(string, ...any) { lines++ })

	resp, err := Client(router, nil).Get(origin.URL)
	if err != nil {
		t.Fatalf("unrouted request failed: %v", err)
	}
	resp.Body.Close()
	if lines != 0 {
		t.Errorf("logged %d lines for an unrouted host, want 0", lines)
	}
}

// TestUpstreamStatusIsNotRetried is the other half of the rule: a response that
// came back through a working proxy is a real answer, even a 403 one, and must
// not trigger a second request on the direct route.
func TestUpstreamStatusIsNotRetried(t *testing.T) {
	var hits int
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer origin.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(origin.URL, "http://"))

	// A router whose "proxy" is a real, working CONNECT-less HTTP proxy is more
	// machinery than this needs; routing to the origin directly and asserting
	// the hit count is what pins the no-double-fetch rule.
	router, err := NewRouter(Options{ProxyURL: "http://samo:t@" + host, Hosts: host})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	resp, err := Client(router, nil).Get(origin.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 passed through", resp.StatusCode)
	}
	if hits != 1 {
		t.Errorf("origin saw %d requests, want 1 — a status is not a retry trigger", hits)
	}
}

func TestRecoveryIsLoggedWhenTheProxyComesBack(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer origin.Close()
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(origin.URL, "http://"))

	var lines []string
	router := deadProxyRouter(t, host, func(format string, args ...any) {
		lines = append(lines, format)
	})
	client := Client(router, nil)
	transport := client.Transport.(*fallbackTransport)

	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	resp.Body.Close()

	// Stand the proxy back up by pointing the proxied transport straight at the
	// origin, and clear the cooldown as time would.
	transport.proxied = newTransport(nil)
	transport.mu.Lock()
	transport.downUntil = time.Time{}
	transport.mu.Unlock()

	resp, err = client.Get(origin.URL)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	resp.Body.Close()

	if len(lines) != 2 {
		t.Fatalf("logged %d lines, want an outage line and a recovery line", len(lines))
	}
	if !strings.Contains(lines[1], "reachable again") {
		t.Errorf("second line = %q, want the recovery notice", lines[1])
	}
	if transport.proxyResting() {
		t.Error("proxy still resting after a success")
	}
}
