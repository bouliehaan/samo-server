package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/bouliehaan/samo-server/internal/users"
)

// Two credentials reach this server, and they are not interchangeable.
//
// A bearer (Authorization: Bearer <token>, or X-Samo-Token) is the account
// credential a client keeps in its own storage and sends as a header. It can do
// anything the account can.
//
// A stream token (?stream_token=, internal/users/streamtokens.go) is a
// 30-minute stand-in for one, minted for the single thing a header cannot do:
// ride inside a URL handed to something that sends none — an <audio src>, an
// <img src>, ExoPlayer, a cast receiver, a lock-screen artwork loader. A URL is
// not a secret. It is written to access logs, passed on in Referer headers,
// kept by proxies and pasted into bug reports. So a stream token opens media
// bytes and nothing else. Accepted anywhere else, one leaked URL could mint a
// permanent token, set a new password, renew itself for ever, or pair a
// samo-radio device and have an admin credential sent wherever it points.
//
// The route table says which is which. handleAPI takes a bearer and nothing
// else, and is the default. handleMedia also takes a stream token, and is only
// for GET routes that serve audio or image bytes. route_auth_test.go fails the
// build for a stream-token route, or a route with no auth at all, that nobody
// wrote a reason for, and keeps everything that reads a stream token in this
// file.

type contextKey string

const principalContextKey contextKey = "samo-principal"

func (s *Server) withPrincipal(ctx context.Context, principal users.Principal) context.Context {
	return context.WithValue(ctx, principalContextKey, principal)
}

func principalFromContext(ctx context.Context) (users.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey).(users.Principal)
	return principal, ok
}

// handleAPI registers a route that needs a bearer.
func (s *Server) handleAPI(pattern string, handler http.HandlerFunc) {
	s.mux.HandleFunc(pattern, s.requireCredential(false, handler))
}

// handleMedia registers a GET route that serves audio or image bytes to a
// consumer that may not be able to send a header, so a stream token opens it
// too. Nothing that changes state, and nothing that answers with anything but
// media, belongs here.
func (s *Server) handleMedia(pattern string, handler http.HandlerFunc) {
	s.mux.HandleFunc(pattern, s.requireCredential(true, handler))
}

func (s *Server) requireCredential(streamTokenAllowed bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := s.authenticateBearer(r)
		if !ok && streamTokenAllowed {
			principal, ok = s.authenticateStreamToken(r)
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="samo"`)
			message := "missing or invalid credentials"
			if !streamTokenAllowed && tokenFromRequest(r) == "" && streamTokenFromRequest(r) != "" {
				// Said whether or not the stream token is valid: a client that
				// sent one to the wrong route needs to learn why, and someone
				// holding one learns nothing they did not already know.
				message = "a stream token only opens media; send the account token in the Authorization header"
			}
			writeError(w, http.StatusUnauthorized, message)
			return
		}
		next(w, r.WithContext(s.withPrincipal(r.Context(), principal)))
	}
}

// authenticateBearer resolves the account credential a request carries in a
// header, and nothing else. A handler registered outside handleAPI that has to
// know who is asking authenticates through this.
func (s *Server) authenticateBearer(r *http.Request) (users.Principal, bool) {
	if s.users == nil || !s.users.Enabled() {
		if s.apiToken == "" || tokenFromRequest(r) == s.apiToken {
			return users.Principal{User: users.User{ID: users.BootstrapUserID, Username: "server", Role: users.RoleAdmin}}, true
		}
		return users.Principal{}, false
	}
	token := tokenFromRequest(r)
	if token == "" {
		return users.Principal{}, false
	}
	principal, err := s.users.AuthenticateToken(r.Context(), token)
	if err != nil {
		return users.Principal{}, false
	}
	return principal, true
}

// authenticateStreamToken resolves ?stream_token= to the user whose bearer
// minted it. Only requireCredential calls it, and only for handleMedia routes.
func (s *Server) authenticateStreamToken(r *http.Request) (users.Principal, bool) {
	if s.users == nil || !s.users.Enabled() {
		return users.Principal{}, false
	}
	streamToken := streamTokenFromRequest(r)
	if streamToken == "" {
		return users.Principal{}, false
	}
	principal, err := s.users.AuthenticateStreamToken(r.Context(), streamToken)
	if err != nil {
		return users.Principal{}, false
	}
	return principal, true
}

func streamTokenFromRequest(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("stream_token"))
}

func (s *Server) currentUser(r *http.Request) (users.Principal, bool) {
	return principalFromContext(r.Context())
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (users.Principal, bool) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return users.Principal{}, false
	}
	if principal.User.Role != users.RoleAdmin {
		writeError(w, http.StatusForbidden, "admin required")
		return users.Principal{}, false
	}
	return principal, true
}

func (s *Server) usersService() *users.Service {
	return s.users
}

// isAdmin answers the same question requireAdmin asks, without answering the
// request. For a response that CONTAINS an admin-only affordance rather than
// being one: a field a non-admin should not be offered is left out, and the
// rest of the payload is served exactly as it always was.
func (s *Server) isAdmin(r *http.Request) bool {
	principal, ok := s.currentUser(r)
	return ok && principal.User.Role == users.RoleAdmin
}
