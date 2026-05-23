package api

import (
	"context"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/users"
)

type contextKey string

const principalContextKey contextKey = "samo-principal"

func (s *Server) withPrincipal(ctx context.Context, principal users.Principal) context.Context {
	return context.WithValue(ctx, principalContextKey, principal)
}

func principalFromContext(ctx context.Context) (users.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey).(users.Principal)
	return principal, ok
}

func (s *Server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := s.authenticateRequest(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="samo"`)
			writeError(w, http.StatusUnauthorized, "missing or invalid credentials")
			return
		}
		next(w, r.WithContext(s.withPrincipal(r.Context(), principal)))
	}
}

func (s *Server) authenticateRequest(r *http.Request) (users.Principal, bool) {
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
