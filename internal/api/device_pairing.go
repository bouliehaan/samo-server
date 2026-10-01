package api

// Device pairing signs a television in without a password typed on a remote.
// It is the OAuth device authorization grant (RFC 8628) cut down to one
// server and one kind of client:
//
//  1. The TV calls start and gets two codes. The device code is a 256-bit
//     secret that only the TV ever holds; it is what redeems the approval. The
//     user code is short and meant to be read off the screen.
//  2. The TV shows the user code and the /pair address. Someone already signed
//     in (a phone, a laptop) opens /pair, enters or scans the user code, and
//     approves it with their own bearer token.
//  3. The TV polls with its device code. The first poll after approval
//     consumes the session and gets a new device token for the approver's
//     account, in exactly the shape a password login returns.
//
// Sessions live in memory: a restart simply expires every pending code, which
// costs the TV one "request a new code". Nothing here outlives ten minutes.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/users"
)

// errPairingBusy is a start refused by the session cap or a rate budget.
var errPairingBusy = errors.New("slow_down")

const pairingLifetime = 10 * time.Minute
const pairingInterval = 5 * time.Second
const pairingAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 32 symbols; no ambiguous 0/1/I/O.

type devicePairing struct {
	code     string
	expires  time.Time
	nextPoll time.Time
	interval time.Duration
	userID   string
	denied   bool
}
type pairingRate struct {
	start time.Time
	count int
}
type devicePairings struct {
	mu       sync.Mutex
	sessions map[[32]byte]*devicePairing
	rates    map[string]pairingRate
}

func newDevicePairings() *devicePairings {
	return &devicePairings{sessions: make(map[[32]byte]*devicePairing), rates: make(map[string]pairingRate)}
}
func (p *devicePairings) sweep(now time.Time) {
	for key, session := range p.sessions {
		if !now.Before(session.expires) {
			delete(p.sessions, key)
		}
	}
	for key, rate := range p.rates {
		if now.Sub(rate.start) >= time.Minute {
			delete(p.rates, key)
		}
	}
}

// Bounded state and independent global/per-caller budgets. The address is the
// TCP peer, not a caller-controlled forwarded header. Proxied installations
// deliberately share that budget rather than allowing spoofed-address bypass.
func (p *devicePairings) allow(key string, max int, now time.Time) bool {
	rate := p.rates[key]
	if now.Sub(rate.start) >= time.Minute {
		rate = pairingRate{start: now}
	}
	if rate.count >= max {
		return false
	}
	if _, exists := p.rates[key]; !exists && len(p.rates) >= 1024 {
		return false
	}
	rate.count++
	p.rates[key] = rate
	return true
}
func pairingPeer(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func normalizePairingCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}
func (p *devicePairings) start(peer string, now time.Time) (secret, code string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweep(now)
	if len(p.sessions) >= 256 || !p.allow("start:global", 100, now) || !p.allow("start:"+peer, 10, now) {
		return "", "", errPairingBusy
	}
	// Retry the extraordinarily unlikely short-code collision rather than letting
	// an approval bind to the wrong device. The polling secret has 256 bits.
	for attempt := 0; attempt < 8; attempt++ {
		var entropy [40]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return "", "", err
		}
		raw := make([]byte, 8)
		for i := range raw {
			raw[i] = pairingAlphabet[int(entropy[32+i])%len(pairingAlphabet)]
		}
		code = string(raw)
		collision := false
		for _, session := range p.sessions {
			if session.code == code {
				collision = true
				break
			}
		}
		if collision {
			continue
		}
		secret = hex.EncodeToString(entropy[:32])
		p.sessions[sha256.Sum256([]byte(secret))] = &devicePairing{code: code, expires: now.Add(pairingLifetime), interval: pairingInterval}
		return secret, code[:4] + "-" + code[4:], nil
	}
	return "", "", errors.New("unable to create pairing code")
}
func (p *devicePairings) decide(code, userID string, approve bool, now time.Time) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweep(now)
	if !p.allow("approve:"+userID, 20, now) {
		return "slow_down"
	}
	code = normalizePairingCode(code)
	for _, session := range p.sessions {
		if session.code != code {
			continue
		}
		if session.userID != "" || session.denied {
			return "invalid_code"
		}
		if approve {
			session.userID = userID
		} else {
			session.denied = true
		}
		return "approved"
	}
	return "invalid_code"
}
func (p *devicePairings) poll(secret string, now time.Time) (userID, status string, interval time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := sha256.Sum256([]byte(secret))
	session := p.sessions[key]
	if session == nil || !now.Before(session.expires) {
		delete(p.sessions, key)
		return "", "expired_token", 0
	}
	if now.Before(session.nextPoll) {
		if session.interval < 30*time.Second {
			session.interval += pairingInterval
		}
		session.nextPoll = now.Add(session.interval)
		return "", "slow_down", session.interval
	}
	if session.denied {
		delete(p.sessions, key)
		return "", "access_denied", 0
	}
	if session.userID != "" {
		// Atomic consume: concurrent/replayed polls can issue at most one token.
		delete(p.sessions, key)
		return session.userID, "approved", 0
	}
	session.nextPoll = now.Add(session.interval)
	return "", "authorization_pending", session.interval
}
func pairingHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}
func (s *Server) startDevicePairing(w http.ResponseWriter, r *http.Request) {
	pairingHeaders(w)
	if s.users == nil || !s.users.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "user accounts are not configured")
		return
	}
	// Mid-setup, a code is a dead end: there may be no account to approve it,
	// and /pair's sign-in bounces to /setup, losing the code on the way. Say
	// so instead, the way /login does; the TV then points at /setup.
	setup, err := s.computeSetupStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if setup.NeedsSetup {
		writeError(w, http.StatusServiceUnavailable, "server setup is not finished")
		return
	}
	secret, code, err := s.devicePairings.start(pairingPeer(r), time.Now())
	if err != nil {
		if errors.Is(err, errPairingBusy) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "slow_down")
		} else {
			writeError(w, http.StatusInternalServerError, "unable to create pairing code")
		}
		return
	}
	// Relative URI: the client keeps the server origin it explicitly selected,
	// never a potentially spoofed Host/Forwarded header from an unauthenticated request.
	writeJSON(w, http.StatusOK, map[string]any{"device_code": secret, "user_code": code, "verification_uri": "/pair", "expires_in": int(pairingLifetime.Seconds()), "interval": int(pairingInterval.Seconds())})
}
func (s *Server) approveDevicePairing(w http.ResponseWriter, r *http.Request) {
	pairingHeaders(w)
	if s.users == nil || !s.users.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "user accounts are not configured")
		return
	}
	// Approval requires a real bearer credential; a media URL's stream token
	// must never be exchanged for a permanent account token.
	principal, err := s.users.AuthenticateToken(r.Context(), tokenFromRequest(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "sign in to approve this device")
		return
	}
	var input struct {
		UserCode string `json:"user_code"`
		Approve  bool   `json:"approve"`
	}
	if !readJSONBody(w, r, &input) {
		return
	}
	status := s.devicePairings.decide(input.UserCode, principal.User.ID, input.Approve, time.Now())
	if status == "slow_down" {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, status)
		return
	}
	if status != "approved" {
		writeError(w, http.StatusBadRequest, "That code is invalid, expired, or already used. Start again on your TV.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"approved": input.Approve})
}
func (s *Server) pollDevicePairing(w http.ResponseWriter, r *http.Request) {
	pairingHeaders(w)
	if s.users == nil || !s.users.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "user accounts are not configured")
		return
	}
	var input struct {
		DeviceCode string `json:"device_code"`
	}
	if !readJSONBody(w, r, &input) {
		return
	}
	if len(input.DeviceCode) != 64 {
		writeError(w, http.StatusBadRequest, "expired_token")
		return
	}
	userID, status, interval := s.devicePairings.poll(input.DeviceCode, time.Now())
	if status != "approved" {
		writeJSON(w, http.StatusOK, map[string]any{"status": status, "interval": int(interval.Seconds())})
		return
	}
	user, err := s.users.Get(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "account is no longer available")
		return
	}
	token, err := s.users.IssueToken(r.Context(), users.Principal{User: user}, users.CreateTokenInput{Label: "samo Android TV"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Unable to finish pairing. Request a new code on the TV.")
		return
	}
	// The same body a password login returns, plus the status every poll
	// carries, so a client turns either one into a session the same way.
	writeJSON(w, http.StatusOK, devicePairingApproval{
		Status: "approved",
		loginResponse: loginResponse{
			LoginResponse: users.LoginResponse{User: user, Token: token.Secret, TokenMeta: token.Token},
			ServerID:      s.serverIdentity(r.Context()),
		},
	})
}

type devicePairingApproval struct {
	Status string `json:"status"`
	loginResponse
}

func (s *Server) devicePairingPage(w http.ResponseWriter, r *http.Request) {
	pairingHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(pairPage)
}

// pairPage is assembled once at startup like the other pages; it takes no
// template data, so it is served as the finished bytes.
var pairPage = []byte(pageSource("pair"))
