package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// loginLimiter throttles repeated failed /api/v1/auth/login attempts. It
// tracks failures under two independent keys — the attempted username and
// the caller's address — so an attacker either grinding one account from
// many addresses or cycling many accounts from one address gets locked out.
// The username key is the real backstop: the address key is best-effort
// (see clientAddr) and only meaningful when the server sits behind a proxy
// that can be trusted to set the forwarded-for header, e.g. the Cloudflare
// tunnel this is designed for.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttemptState
	maxTries int
	window   time.Duration
	lockout  time.Duration
}

type loginAttemptState struct {
	count       int
	windowStart time.Time
	lockedUntil time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		attempts: make(map[string]*loginAttemptState),
		maxTries: 5,
		window:   5 * time.Minute,
		lockout:  15 * time.Minute,
	}
}

// allow reports whether an attempt under key may proceed right now, and if
// not, how long the caller should wait before retrying.
func (l *loginLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state, ok := l.attempts[key]
	if !ok {
		return true, 0
	}
	if now.Before(state.lockedUntil) {
		return false, state.lockedUntil.Sub(now)
	}
	return true, 0
}

func (l *loginLimiter) recordFailure(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)
	state, ok := l.attempts[key]
	if !ok || now.Sub(state.windowStart) > l.window {
		state = &loginAttemptState{windowStart: now}
		l.attempts[key] = state
	}
	state.count++
	if state.count >= l.maxTries {
		state.lockedUntil = now.Add(l.lockout)
	}
}

func (l *loginLimiter) recordSuccess(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

// sweepLocked evicts stale entries once the table has grown large enough
// that an attacker cycling through usernames/addresses could otherwise grow
// it without bound. Caller must hold l.mu.
func (l *loginLimiter) sweepLocked(now time.Time) {
	const sweepThreshold = 10000
	if len(l.attempts) < sweepThreshold {
		return
	}
	for key, state := range l.attempts {
		if now.After(state.lockedUntil) && now.Sub(state.windowStart) > l.window {
			delete(l.attempts, key)
		}
	}
}

// clientAddr extracts a best-effort client address for rate-limiting
// purposes. CF-Connecting-IP is trustworthy when the server is only reachable
// through a Cloudflare tunnel (the deployment this hardening targets);
// X-Forwarded-For is a generic fallback for other reverse proxies. Neither is
// authoritative if the server is also reachable directly, which is why the
// per-username limit — not this one — is the real protection.
func clientAddr(r *http.Request) string {
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		return cf
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		first := strings.SplitN(xff, ",", 2)[0]
		if addr := strings.TrimSpace(first); addr != "" {
			return addr
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
