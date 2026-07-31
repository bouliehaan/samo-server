package api

import (
	"net/http"
	"strings"
)

// contentSecurityPolicy matches what the three HTML pages (login, setup, app)
// actually load: everything is same-origin. The UI font (Office Code Pro) is
// embedded in the binary and served from /assets/fonts, so there is no Google
// Fonts / gstatic origin to allow.
//
// script-src no longer needs 'unsafe-inline'. The pages used to carry their
// JavaScript in inline <script> blocks; it now ships as hashed module bundles
// under /assets/build, and none of the three pages uses an inline event
// handler. That closes the single largest hole in this policy: an injected
// <script> in any interpolated string is now inert.
//
// style-src still needs it. The dashboard writes 43 inline style="" attributes
// through innerHTML (progress bars, cover positioning), and style-src governs
// attributes as well as <style> blocks. Moving those to CSS custom properties
// would let this tighten too — a follow-up, not a drive-by change.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
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
