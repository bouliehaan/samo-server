// Package egress builds an HTTP client that sends a named few hosts through an
// outbound proxy while leaving every other request on the default route.
//
// # Why this exists
//
// The samo box is expected to run behind a VPN with a kill-switch, and that is
// the right posture: it is what makes the box safe to run. Some CDNs refuse
// commercial VPN exit addresses outright, though, and when they do the failure
// is invisible from inside — the API that finds a resource answers normally and
// only the fetch of the resource itself is refused.
//
// Deezer's artist artwork is the case this was written for. api.deezer.com
// serves a VPN exit address byte-identical results, while cdn-images.dzcdn.net
// answers every request from the same address with 403. The artist photo
// backfill therefore finds a picture for each artist and then cannot download
// a single one of them.
//
// # The shape of the fix
//
// A companion process on a box whose default route is the plain WAN (see the
// samo-proxy repo) offers a CONNECT proxy restricted to an allow-list. This
// package is the client half: it routes *only* the allow-listed hosts through
// that proxy and leaves everything else — every other metadata provider, every
// podcast feed, every scrobble — on the VPN exactly as before.
//
// # The tradeoff, stated rather than buried
//
// Traffic that goes through the proxy does not go through the VPN. A request to
// an artwork CDN carries the identifier of something in the library, so an
// observer on that path learns a little about what the library contains. That
// is more than they learned when the answer was "nothing at all", and it is why
// this is off unless deliberately configured, and why the host list is closed
// rather than a default-open policy with exceptions.
package egress

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Options configures the client. An empty ProxyURL disables the feature and
// Client returns the base client untouched, which is what makes this safe to
// wire unconditionally at startup.
type Options struct {
	// ProxyURL is the CONNECT proxy, including the shared secret as userinfo,
	// e.g. http://samo:token@192.168.1.12:6768. Go's transport turns that into
	// the Proxy-Authorization header on the CONNECT itself.
	ProxyURL string

	// Hosts is the comma-separated closed list of hosts to route through the
	// proxy. An entry is either an exact host or a suffix with a leading dot.
	Hosts string

	// Base is the client to derive from. Its timeout is preserved; only the
	// transport's proxy function is replaced. Nil means a sensible default.
	Base *http.Client

	// Logger records when the proxy starts and stops being usable. Optional,
	// but worth wiring: a proxy that has quietly gone away looks exactly like
	// the bug this package was written to fix, and the log line is the only
	// thing that tells the two apart.
	Logger func(format string, args ...any)
}

// DefaultHosts is the list this was written for: Deezer's image CDN and its
// aliases, the only hosts observed to refuse a VPN exit address while the API
// that names them serves it happily.
//
// Deliberately not "everything Deezer": api.deezer.com works fine over the VPN
// and stays there, so only the download hop leaves. Keeping the lookup on the
// VPN is what keeps the search terms — which are artist names straight out of
// the library — off the plain WAN.
var DefaultHosts = []string{
	"cdn-images.dzcdn.net",
	"e-cdns-images.dzcdn.net",
	"cdns-images.dzcdn.net",
}

// Router decides, per request, whether to use the proxy.
type Router struct {
	proxy    *url.URL
	exact    map[string]struct{}
	suffixes []string
	logger   func(format string, args ...any)
}

func (r *Router) log(format string, args ...any) {
	if r == nil || r.logger == nil {
		return
	}
	r.logger(format, args...)
}

// Enabled reports whether any request would actually be proxied.
func (r *Router) Enabled() bool {
	return r != nil && r.proxy != nil && (len(r.exact) > 0 || len(r.suffixes) > 0)
}

// Hosts renders the routed list for the startup log, so what leaves the VPN is
// visible in the log rather than only in the environment.
func (r *Router) Hosts() string {
	if r == nil {
		return ""
	}
	names := make([]string, 0, len(r.exact)+len(r.suffixes))
	for host := range r.exact {
		names = append(names, host)
	}
	names = append(names, r.suffixes...)
	sort.Strings(names)
	return strings.Join(names, ",")
}

// ProxyHost is the proxy's host:port with the credential stripped, for logging.
// The token must never reach a log line, and a URL logged whole would carry it.
func (r *Router) ProxyHost() string {
	if !r.Enabled() {
		return ""
	}
	return r.proxy.Host
}

// Routes reports whether host would be sent through the proxy.
func (r *Router) Routes(host string) bool {
	if !r.Enabled() {
		return false
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return false
	}
	if _, ok := r.exact[host]; ok {
		return true
	}
	for _, suffix := range r.suffixes {
		// The suffix carries its leading dot, so this cannot match a sibling
		// name that merely ends in the same characters.
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// proxyFor is the http.Transport Proxy hook. Returning (nil, nil) means "no
// proxy", which is the answer for everything not on the list — that default is
// the whole safety property of this package.
func (r *Router) proxyFor(request *http.Request) (*url.URL, error) {
	if request == nil || request.URL == nil {
		return nil, nil
	}
	if !r.Routes(request.URL.Hostname()) {
		return nil, nil
	}
	return r.proxy, nil
}

// NewRouter parses the configuration. A blank ProxyURL yields a disabled
// router and no error, so callers can wire this unconditionally.
func NewRouter(options Options) (*Router, error) {
	raw := strings.TrimSpace(options.ProxyURL)
	if raw == "" {
		return &Router{}, nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("egress proxy URL %q: %w", raw, err)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("egress proxy URL %q: scheme must be http or https", raw)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("egress proxy URL %q: no host", raw)
	}

	hosts := strings.TrimSpace(options.Hosts)
	if hosts == "" {
		hosts = strings.Join(DefaultHosts, ",")
	}
	router := &Router{proxy: parsed, exact: map[string]struct{}{}, logger: options.Logger}
	for _, part := range strings.Split(hosts, ",") {
		entry := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(part)), ".")
		if entry == "" {
			continue
		}
		if strings.ContainsAny(entry, "/:") {
			return nil, fmt.Errorf("egress host list: %q looks like a URL, not a host", entry)
		}
		if entry == "." || entry == "*" {
			return nil, fmt.Errorf("egress host list: %q would route everything off the VPN", entry)
		}
		if strings.HasPrefix(entry, "*.") {
			entry = entry[1:]
		}
		if strings.HasPrefix(entry, ".") {
			router.suffixes = append(router.suffixes, entry)
			continue
		}
		router.exact[entry] = struct{}{}
	}
	if !router.Enabled() {
		return nil, fmt.Errorf("egress proxy is set but the host list is empty")
	}
	sort.Strings(router.suffixes)
	return router, nil
}

// Client returns an HTTP client that prefers the proxy for the allow-listed
// hosts and falls back to the box's ordinary route whenever the proxy cannot be
// used. When the router is disabled it returns the base client unchanged, so
// there is exactly one code path at the call site.
func Client(router *Router, base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{Timeout: 20 * time.Second}
	}
	if !router.Enabled() {
		return base
	}

	routed := *base
	routed.Transport = &fallbackTransport{
		router:   router,
		proxied:  newTransport(router.proxyFor),
		direct:   newTransport(nil),
		cooldown: proxyCooldown,
	}
	if routed.Timeout == 0 {
		routed.Timeout = 20 * time.Second
	}
	return &routed
}

// proxyCooldown is how long the proxy is skipped after it fails to carry a
// request. Without it a backfill of a few hundred artists would pay the proxy's
// dial timeout once per artist — turning "the helper box is off" into an hour
// of waiting instead of a run that simply falls back and gets on with it.
const proxyCooldown = 2 * time.Minute

func newTransport(proxy func(*http.Request) (*url.URL, error)) *http.Transport {
	return &http.Transport{
		Proxy: proxy,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// fallbackTransport sends allow-listed hosts through the proxy and everything
// else straight out, and — the point of this type — retries directly when the
// proxy itself could not carry the request.
//
// Falling back is unambiguously the right default here, because "direct" on
// this box means "through the VPN". The proxy is the path that leaves the VPN;
// the fallback is the more private one, not the less. So there is nothing to
// weigh: if the helper box is off, missing, or refusing, the worst case is that
// artist photos go back to failing exactly as they did before this existed,
// which is strictly better than the request erroring out.
//
// The distinction that matters is *error* versus *response*. A transport error
// means the proxy could not be used — it is down, unreachable, refusing the
// CONNECT, or rejecting the token — and that is worth retrying directly. An
// HTTP response, including a 403 from the CDN, is a real answer that came back
// through a working proxy; retrying that directly would just be a second
// request to a host already known to refuse this address.
type fallbackTransport struct {
	router   *Router
	proxied  http.RoundTripper
	direct   http.RoundTripper
	cooldown time.Duration

	mu        sync.Mutex
	downUntil time.Time
	announced bool
}

func (t *fallbackTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !t.router.Routes(request.URL.Hostname()) {
		return t.direct.RoundTrip(request)
	}
	if t.proxyResting() {
		return t.direct.RoundTrip(request)
	}

	// Only a replayable request may be retried. Image downloads are bodiless
	// GETs, so this is nearly always true; anything else is sent once through
	// the proxy and its error reported honestly rather than silently resent.
	replayable := request.Body == nil || request.Body == http.NoBody || request.GetBody != nil

	attempt := request
	if replayable {
		attempt = request.Clone(request.Context())
		if request.GetBody != nil {
			body, err := request.GetBody()
			if err != nil {
				return t.proxied.RoundTrip(request)
			}
			attempt.Body = body
		}
	}

	response, err := t.proxied.RoundTrip(attempt)
	if err == nil {
		t.markUp()
		return response, nil
	}
	if !replayable {
		return nil, err
	}

	// The context being done is not the proxy's fault, and retrying would only
	// produce the same error a second time.
	if ctxErr := request.Context().Err(); ctxErr != nil {
		return nil, err
	}

	t.markDown(request.URL.Hostname(), err)

	retry := request.Clone(request.Context())
	if request.GetBody != nil {
		body, bodyErr := request.GetBody()
		if bodyErr != nil {
			return nil, err
		}
		retry.Body = body
	}
	return t.direct.RoundTrip(retry)
}

// proxyResting reports whether the proxy is inside its cooldown window.
func (t *fallbackTransport) proxyResting() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return time.Now().Before(t.downUntil)
}

func (t *fallbackTransport) markDown(host string, cause error) {
	t.mu.Lock()
	first := !t.announced
	t.announced = true
	t.downUntil = time.Now().Add(t.cooldown)
	t.mu.Unlock()

	// Once per outage rather than once per request: a backfill would otherwise
	// write the same line hundreds of times.
	if first {
		t.router.log(
			"egress proxy %s unusable, falling back to the default route for %s (retrying in %s): %v",
			t.router.ProxyHost(), host, t.cooldown, cause,
		)
	}
}

func (t *fallbackTransport) markUp() {
	t.mu.Lock()
	recovered := t.announced
	t.announced = false
	t.downUntil = time.Time{}
	t.mu.Unlock()

	if recovered {
		t.router.log("egress proxy %s is reachable again", t.router.ProxyHost())
	}
}
