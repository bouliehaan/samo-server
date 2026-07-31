package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/users"
)

// issueStreamToken mints a short-lived credential the dashboard can put in
// stream/cover URLs so HTML5 audio/img elements don't need to send the
// bearer header (which they can't). The caller must already be
// authenticated via the standard requireUser path.
func (s *Server) issueStreamToken(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	service := s.usersService()
	if service == nil || !service.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "user accounts are not configured")
		return
	}
	token, expiresAt, err := service.IssueStreamToken(principal.User.ID)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"expiresAt": expiresAt,
	})
}

func (s *Server) loginUser(w http.ResponseWriter, r *http.Request) {
	service := s.usersService()
	if service == nil || !service.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "user accounts are not configured")
		return
	}
	var input users.LoginInput
	if !readJSONBody(w, r, &input) {
		return
	}

	now := time.Now()
	usernameKey := "user:" + strings.ToLower(strings.TrimSpace(input.Username))
	addrKey := "addr:" + clientAddr(r)
	if ok, retryAfter := s.loginLimiter.allow(usernameKey, now); !ok {
		writeLoginRateLimited(w, retryAfter)
		return
	}
	if ok, retryAfter := s.loginLimiter.allow(addrKey, now); !ok {
		writeLoginRateLimited(w, retryAfter)
		return
	}

	response, err := service.Login(r.Context(), input)
	if err != nil {
		s.loginLimiter.recordFailure(usernameKey, now)
		s.loginLimiter.recordFailure(addrKey, now)
		writeUserError(w, err)
		return
	}
	s.loginLimiter.recordSuccess(usernameKey)
	s.loginLimiter.recordSuccess(addrKey)
	writeJSON(w, http.StatusOK, loginResponse{
		LoginResponse: response,
		ServerID:      s.serverIdentity(r.Context()),
	})
}

// loginResponse carries the server's stable identity alongside the credential
// so a client can key its local state by the server rather than by the address
// it happened to connect over. Embedded, so the wire shape stays a superset of
// users.LoginResponse and older clients are unaffected.
type loginResponse struct {
	users.LoginResponse
	ServerID string `json:"serverId,omitempty"`
}

func writeLoginRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(w, http.StatusTooManyRequests, "too many failed login attempts; try again later")
}

func (s *Server) getCurrentUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, principal.User)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	items, err := s.usersService().List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var input users.CreateUserInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.usersService().Create(r.Context(), principal, input)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateCurrentUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input users.UpdateUserInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.usersService().Update(r.Context(), principal, principal.User.ID, input)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listUserTokens(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := s.usersService().ListTokens(r.Context(), principal)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createUserToken(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input users.CreateTokenInput
	if !readJSONBody(w, r, &input) {
		return
	}
	issued, err := s.usersService().IssueToken(r.Context(), principal, input)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, issued)
}

func (s *Server) revokeUserToken(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.usersService().RevokeToken(r.Context(), principal, r.PathValue("id")); err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

func writeUserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, users.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, users.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, users.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, users.ErrInvalidUsername), errors.Is(err, users.ErrInvalidPassword), errors.Is(err, users.ErrInvalidToken):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, users.ErrUsernameTaken):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// Subsonic credential management.
//
// The Subsonic protocol's default auth mode needs a password the server can
// recover, which bcrypt deliberately prevents. Rather than weaken login
// security, Subsonic gets its own generated app password: opt-in, revocable,
// and useless for anything but browsing and streaming that user's library.

type subsonicCredentialResponse struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username"`
	// Password is returned only at the moment it is generated, the same way an
	// API token is. It is not recoverable through the API afterwards.
	Password string `json:"password,omitempty"`
}

func (s *Server) getSubsonicCredential(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	enabled, err := s.usersService().SubsonicEnabled(r.Context(), principal.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, subsonicCredentialResponse{
		Enabled:  enabled,
		Username: principal.User.Username,
	})
}

func (s *Server) createSubsonicCredential(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	password, err := s.usersService().GenerateSubsonicPassword(r.Context(), principal)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, subsonicCredentialResponse{
		Enabled:  true,
		Username: principal.User.Username,
		Password: password,
	})
}

func (s *Server) deleteSubsonicCredential(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.usersService().ClearSubsonicPassword(r.Context(), principal); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
