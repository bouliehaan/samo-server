package api

import (
	"net/http"
	"strings"
)

// WithCORS wraps the API so desktop and mobile webviews can call a remote
// Samo Server from a different origin during development and in packaged apps.
//
// Origin is reflected rather than restricted to a fixed allowlist because
// legitimate callers (Electron shells, local dev servers on arbitrary ports)
// span origins we can't enumerate ahead of time. That's only safe because
// auth is bearer-token-only (Authorization/X-Samo-Token header or a
// short-lived stream_token query param) with no cookie involved — a
// cross-origin page can't make the browser attach a token it never had
// access to. Don't reintroduce Access-Control-Allow-Credentials unless a
// cookie-based session is added alongside a real origin allowlist; paired
// with reflected origins it turns every authenticated route into a
// same-origin-policy bypass for any site the victim's browser visits.
func WithCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Samo-Token, Accept")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Type, Content-Length, Content-Disposition, WWW-Authenticate")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
