package api

import (
	_ "embed"
	"net/http"
)

// Office Code Pro — the one typeface the whole server UI is set in. It is the
// same face the Android and desktop clients use for mono/label chrome, so the
// three surfaces read as one product.
//
// The files are embedded rather than pulled from a CDN on purpose: Samo Server
// is usually reached over a LAN (often with no working DNS or upstream route),
// and a self-hosted media server that renders unstyled until Google Fonts
// answers is a bad look. Self-hosting also lets the CSP drop its
// fonts.googleapis.com / fonts.gstatic.com allowances entirely — see
// contentSecurityPolicy in security_headers.go.
//
// Office Code Pro is Chris Simpkins' Source Code Pro derivative, distributed
// under the SIL Open Font License 1.1. The same two files ship in the clients'
// assets/fonts directories.
//
//go:embed assets/fonts/officecodepro-regular.otf
var officeCodeProRegular []byte

//go:embed assets/fonts/officecodepro-bold.otf
var officeCodeProBold []byte

// serveFont writes an embedded OpenType face. Fonts are content-addressed by
// filename and never change within a build, so they get the same long,
// immutable cache lifetime as the favicons. The route is public: the login and
// setup pages need the face before any token exists.
func serveFont(otf []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "font/otf")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(otf)
	}
}
