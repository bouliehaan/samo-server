package subsonic

import (
	"context"
	"net/http"
	"strings"

	"github.com/bouliehaan/samo-server/internal/users"
)

type contextKey string

const principalContextKey contextKey = "subsonic-principal"

func (s *Server) authorize(r *http.Request) (*http.Request, bool) {
	if s.users != nil && s.users.Enabled() {
		username := strings.TrimSpace(r.URL.Query().Get("u"))
		password := strings.TrimSpace(r.URL.Query().Get("p"))
		token := strings.TrimSpace(r.URL.Query().Get("t"))
		salt := strings.TrimSpace(r.URL.Query().Get("s"))
		principal, err := s.users.AuthenticateSubsonic(r.Context(), username, password, token, salt)
		if err != nil {
			return r, false
		}
		return r.WithContext(context.WithValue(r.Context(), principalContextKey, principal)), true
	}
	if s.apiToken == "" || legacyAuthorized(r, s.apiToken) {
		principal := users.Principal{User: users.User{ID: users.BootstrapUserID, Username: "server", Role: users.RoleAdmin}}
		return r.WithContext(context.WithValue(r.Context(), principalContextKey, principal)), true
	}
	return r, false
}

func principalFromContext(ctx context.Context) (users.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey).(users.Principal)
	return principal, ok
}

func legacyAuthorized(r *http.Request, apiToken string) bool {
	if token := bearerToken(r); token == apiToken {
		return true
	}
	password := strings.TrimSpace(r.URL.Query().Get("p"))
	if password == apiToken {
		return true
	}
	token := strings.TrimSpace(r.URL.Query().Get("t"))
	salt := strings.TrimSpace(r.URL.Query().Get("s"))
	if token != "" && salt != "" {
		return users.VerifyTokenAuth(apiToken, salt, token)
	}
	return false
}

func bearerToken(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[len("bearer "):])
	}
	return strings.TrimSpace(r.Header.Get("X-Samo-Token"))
}
