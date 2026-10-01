package api

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// Generated from the Samo desktop renderer with scripts/build-web-client.mjs.
// Kept separate from the admin bundle so make ui does not remove it.
//
//go:embed web-client
var webClientFS embed.FS

func (s *Server) webClientHandler() http.Handler {
	sub, err := fs.Sub(webClientFS, "web-client")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/listen/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/listen/" {
			if status, err := s.computeSetupStatus(r.Context()); err == nil && status.NeedsSetup {
				http.Redirect(w, r, "/setup", http.StatusFound)
				return
			}
		}
		// The desktop renderer uses blob audio/workers and embedded font data.
		// Script execution remains restricted to same-origin external modules.
		policy := strings.Replace(contentSecurityPolicy, "font-src 'self'", "font-src 'self' data:", 1)
		policy = strings.Replace(policy, "media-src 'self'", "media-src 'self' blob: data:", 1)
		policy = strings.Replace(policy, "img-src 'self' data:", "img-src 'self' blob: data:", 1)
		w.Header().Set("Content-Security-Policy", policy+"; worker-src 'self' blob:")
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}
