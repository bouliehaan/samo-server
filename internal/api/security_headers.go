package api

import (
	"net/http"
	"strings"
)

// contentSecurityPolicy matches what the three hand-rolled HTML pages
// (login, setup, app) actually load: everything is same-origin. The UI font
// (Office Code Pro) is embedded in the binary and served from /assets/fonts,
// so there is no longer any Google Fonts / gstatic origin to allow. All three
// pages rely on inline <script>/<style> blocks rather than external files, so
// script-src/style-src keep 'unsafe-inline' — tightening that to a nonce-based
// policy would require threading a per-request nonce through every template
// and is a follow-up, not a drive-by change. frame-ancestors/object-src/
// base-uri still block clickjacking, plugin-based execution, and <base>
// injection regardless.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"font-src 'self'; " +
	// Album/podcast/artist art can be a remote URL (podcast feed images are
	// served as a redirect to the origin host, not proxied) - allow https image
	// sources so covers actually render. Still no http: (mixed content) and
	// still image-only, so this stays a tight, targeted allowance.
	"img-src 'self' data: https:; " +
	"media-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"frame-ancestors 'none'"

// WithSecurityHeaders sets baseline browser-facing hardening headers on
// every response. Samo Server is now reachable straight from the public
// internet through a Cloudflare tunnel, so the login/setup/app HTML it
// serves needs the same headers any internet-facing site needs.
func WithSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")

		// HSTS is only meaningful (and only honored by browsers) on a
		// connection the browser itself sees as secure. The Go server always
		// sees plain HTTP — cloudflared terminates TLS at the edge and
		// forwards over the tunnel — so we trust the same X-Forwarded-Proto
		// signal publicURL() already uses to detect that. Plain LAN access
		// never sets this header and never sends X-Forwarded-Proto, so it's
		// unaffected.
		if isForwardedHTTPS(r) {
			header.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

func isForwardedHTTPS(r *http.Request) bool {
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	proto = strings.SplitN(proto, ",", 2)[0]
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}
