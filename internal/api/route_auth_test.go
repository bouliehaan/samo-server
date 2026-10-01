package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This test is the enforcement half of the credential rule in auth.go: a
// stream token opens media bytes and nothing else. handleAPI makes the safe
// registration the default; this makes the other two — handleMedia, and a bare
// s.mux registration that no credential check guards at all — decisions with a
// written reason.
//
// It exists because the rule had already been understood once, and applied to
// one route. approveDevicePairing re-authenticated with a bearer so that "a
// media URL's stream token must never be exchanged for a permanent account
// token", while the route that mints permanent tokens, the one that sets a new
// password, and the one that pairs a samo-radio device (handing it an admin
// token) all took a stream token, because every route registered through
// handleAPI did.
//
// If this fails on a route you just added: register it with handleAPI. If it
// has to open from a URL — an <audio> or <img> element, ExoPlayer, a cast
// receiver, a notification's artwork loader — it is media: register it with
// handleMedia and add it to streamTokenRoutes, naming what needs it. If it
// needs no credential at all, add it to publicRoutes with the reason. Do not
// delete the assertion.

// streamTokenRoutes are the routes a ?stream_token= opens. Each is a GET that
// answers with audio or image bytes, and each is listed with what opens it
// without being able to send a header.
var streamTokenRoutes = map[string]string{
	"GET /api/v1/music/tracks/{id}/stream":      "<audio>, ExoPlayer, cast receiver",
	"GET /api/v1/audiobooks/{id}/stream":        "<audio>, ExoPlayer, cast receiver",
	"GET /api/v1/podcasts/episodes/{id}/stream": "<audio>, ExoPlayer, cast receiver",
	"GET /api/v1/media/files/{id}/stream":       "a track's own file; the bytes the track stream already serves",
	"GET /api/v1/channels/{id}/stream":          "<audio>, ExoPlayer; clients re-token it like any /api/v1 stream",
	"GET /channels/{id}/stream":                 "the dashboard's <audio>, external players",
	"GET /channels/{id}/playlist.m3u":           "external players opening a channel from a URL",
	"GET /api/v1/music/albums/{id}/cover":       "<img>, lock-screen and notification artwork, cast receiver",
	"GET /api/v1/music/artists/{id}/cover":      "<img>, lock-screen and notification artwork, cast receiver",
	"GET /api/v1/music/playlists/{id}/cover":    "<img>, lock-screen and notification artwork, cast receiver",
	"GET /api/v1/audiobooks/{id}/cover":         "<img>, lock-screen and notification artwork, cast receiver",
	"GET /api/v1/podcasts/shows/{id}/cover":     "<img>, lock-screen and notification artwork, cast receiver",
	"GET /api/v1/media/images/{id}/image":       "<img>, lock-screen and notification artwork, cast receiver",
	"GET /api/v1/media/covers/{id}/image":       "<img>, lock-screen and notification artwork, cast receiver",
	"GET /api/v1/media/covers/{id}":             "the same bytes as .../image, at its older address",
	"GET /api/v1/explo/art/{id}":                "<img> beside a Search for new result",
}

// publicRoutes are registered on s.mux directly, so nothing checks a credential
// before the handler runs. Some check one themselves, and say which.
var publicRoutes = map[string]string{
	"GET /listen/":           "desktop web client shell and static assets; data and media require account credentials",
	"GET /":                  "redirects to /setup or /app",
	"GET /app":               "the web app's shell page; it signs in with its own bearer",
	"GET /app/":              "the web app's shell page; it signs in with its own bearer",
	"GET /login":             "the sign-in page",
	"GET /login/":            "the sign-in page",
	"GET /setup":             "the first-run wizard page",
	"GET /setup/":            "the first-run wizard page",
	"GET /pair":              "the device approval page; approving is authenticated",
	"GET /health":            "liveness probe",
	"GET /favicon-light.png": "fetched before sign-in",
	"GET /favicon-dark.png":  "fetched before sign-in",
	"GET /favicon.ico":       "fetched before sign-in",
	"GET /assets/build/":     "the pages' scripts and styles, needed before sign-in",
	"GET /assets/fonts/officecodepro-regular.otf": "needed before sign-in",
	"GET /assets/fonts/officecodepro-bold.otf":    "needed before sign-in",

	"POST /api/v1/auth/login":          "exchanges a password for a bearer; rate limited per account and address",
	"POST /api/v1/auth/device/start":   "a TV asks for a code before it holds any credential",
	"POST /api/v1/auth/device/poll":    "the device code in the body is the credential",
	"POST /api/v1/auth/device/approve": "authenticates a bearer itself, never a stream token",

	"GET /api/v1/setup/status":                  "the wizard reads it before any account exists; says only needsSetup afterwards",
	"POST /api/v1/setup/admin":                  "creates the first admin, and refuses once one exists",
	"GET /api/v1/setup/directories":             "open until an admin finishes setup, then requireAdmin",
	"POST /api/v1/setup/libraries":              "allowSetupOrAdmin: the step-one admin bearer",
	"POST /api/v1/setup/scan":                   "allowSetupOrAdmin: the step-one admin bearer",
	"POST /api/v1/setup/complete":               "allowSetupOrAdmin: the step-one admin bearer",
	"GET /radio/{id}/playlist.m3u":              "programmed radio streams are public by design (docs/radio.md)",
	"GET /radio/{id}/stream":                    "programmed radio streams are public by design (docs/radio.md)",
	"GET /internet-radio/{id}/playlist.m3u":     "a relay of a station that is public at its source",
	"GET /internet-radio/{id}/stream":           "a relay of a station that is public at its source",
	"GET /internet-radio/directory/{id}/stream": "a relay of a station that is public at its source",
}

// muxDelegates are calls that hand s.mux to another package, which registers
// routes this scan cannot see. Each authenticates on its own terms.
var muxDelegates = map[string]string{
	"Register": "Subsonic /rest/*: its own u/p/t/s credentials (users.AuthenticateSubsonic); never reads a stream token",
}

// mediaSuffixes name a route that serves media bytes. A GET route shaped like
// one but registered with handleAPI is one no header-less consumer can open,
// which fails silently: a lock screen without artwork, a cast that never
// starts.
var mediaSuffixes = []string{"/stream", "/cover", "/image", ".m3u"}

func TestEveryRouteDeclaresItsCredential(t *testing.T) {
	pkg := parseAPIPackage(t)

	type registration struct {
		pattern string
		where   string
	}
	var bearerRoutes, mediaRoutes, bareRoutes []registration
	delegates := map[string]bool{}
	var offenders []string

	for fileName, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// The registration helpers are where a pattern variable reaches the
			// mux; every call to them is examined below instead.
			if fn.Recv != nil && (fn.Name.Name == "handleAPI" || fn.Name.Name == "handleMedia") {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				where := fileName + ":" + strconv.Itoa(pkg.fset.Position(call.Pos()).Line)
				for _, arg := range call.Args {
					if isMuxSelector(arg) && !isMuxRegistration(call) {
						name := selectorName(call.Fun)
						delegates[name] = true
						if _, ok := muxDelegates[name]; !ok {
							offenders = append(offenders, where+": s.mux handed to "+name+", which registers routes this scan cannot see (add it to muxDelegates with how those routes authenticate)")
						}
					}
				}
				if len(call.Args) == 0 {
					return true
				}
				switch {
				case isMuxRegistration(call):
					pattern, ok := literalPattern(call.Args[0])
					if !ok {
						offenders = append(offenders, where+": a route pattern this scan cannot read")
						return true
					}
					bareRoutes = append(bareRoutes, registration{pattern, where})
				case selectorName(call.Fun) == "handleMedia":
					pattern, ok := literalPattern(call.Args[0])
					if !ok {
						offenders = append(offenders, where+": handleMedia with a pattern this scan cannot read; spell it out")
						return true
					}
					mediaRoutes = append(mediaRoutes, registration{pattern, where})
				case selectorName(call.Fun) == "handleAPI":
					// Any pattern is safe here: a bearer is the default. A pattern
					// built by concatenation is kept only for the media check.
					pattern, _ := stringLit(call.Args[0])
					bearerRoutes = append(bearerRoutes, registration{pattern, where})
				}
				return true
			})
		}
	}

	if len(bearerRoutes) == 0 || len(mediaRoutes) == 0 || len(bareRoutes) == 0 {
		t.Fatalf("found %d handleAPI, %d handleMedia and %d bare routes; the route-table scan has stopped working",
			len(bearerRoutes), len(mediaRoutes), len(bareRoutes))
	}

	registeredMedia := map[string]bool{}
	for _, route := range mediaRoutes {
		registeredMedia[route.pattern] = true
		if !strings.HasPrefix(route.pattern, "GET ") {
			offenders = append(offenders, route.where+": "+route.pattern+" opens with a stream token, and only a GET may; a URL credential never authorizes a change")
			continue
		}
		if _, ok := streamTokenRoutes[route.pattern]; !ok {
			offenders = append(offenders, route.where+": "+route.pattern+" opens with a stream token but is not in streamTokenRoutes")
		}
	}
	registeredBare := map[string]bool{}
	for _, route := range bareRoutes {
		registeredBare[route.pattern] = true
		if _, ok := publicRoutes[route.pattern]; !ok {
			offenders = append(offenders, route.where+": "+route.pattern+" is registered on s.mux directly, past handleAPI and handleMedia, and is not in publicRoutes")
		}
	}
	for _, route := range bearerRoutes {
		if !strings.HasPrefix(route.pattern, "GET ") {
			continue
		}
		for _, suffix := range mediaSuffixes {
			if strings.HasSuffix(route.pattern, suffix) {
				offenders = append(offenders, route.where+": "+route.pattern+" serves media but refuses a stream token, so nothing that cannot send a header can open it; use handleMedia")
			}
		}
	}

	// A reason that outlives its route reads as a decision still in force.
	for pattern := range streamTokenRoutes {
		if !registeredMedia[pattern] {
			offenders = append(offenders, "streamTokenRoutes: "+pattern+" is not registered with handleMedia")
		}
	}
	for pattern := range publicRoutes {
		if !registeredBare[pattern] {
			offenders = append(offenders, "publicRoutes: "+pattern+" is not registered on s.mux")
		}
	}
	for name := range muxDelegates {
		if !delegates[name] {
			offenders = append(offenders, "muxDelegates: nothing hands s.mux to "+name)
		}
	}
	sort.Strings(offenders)

	if len(offenders) > 0 {
		t.Fatalf(`routes whose credential nobody decided:

    %s

handleAPI (a bearer, and nothing else) is the answer for everything that is not
media bytes. handleMedia also takes a ?stream_token=, which is a URL credential:
it ends up in access logs, Referer headers and proxies, so a route that takes one
must be a GET that answers with audio or image bytes, listed in streamTokenRoutes
with what opens it. A route on s.mux with no credential check belongs in
publicRoutes with its reason. See auth.go.`,
			strings.Join(offenders, "\n    "))
	}
}

// TestOnlyAuthGoReadsStreamTokens keeps the rule in one place. A handler that
// read ?stream_token= itself, or called the stream-token check, would accept
// one on a route the route table says takes a bearer.
func TestOnlyAuthGoReadsStreamTokens(t *testing.T) {
	pkg := parseAPIPackage(t)
	guarded := map[string]bool{
		"AuthenticateStreamToken": true,
		"authenticateStreamToken": true,
		"streamTokenFromRequest":  true,
		"requireCredential":       true,
	}

	var offenders []string
	for fileName, file := range pkg.Files {
		if strings.HasSuffix(fileName, "auth.go") {
			continue
		}
		where := func(node ast.Node) string {
			return fileName + ":" + strconv.Itoa(pkg.fset.Position(node.Pos()).Line)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.Ident:
				if guarded[node.Name] {
					offenders = append(offenders, where(node)+": "+node.Name)
				}
			case *ast.BasicLit:
				if value, ok := stringLit(node); ok && strings.Contains(value, "stream_token") {
					offenders = append(offenders, where(node)+": the stream_token parameter")
				}
			}
			return true
		})
	}
	sort.Strings(offenders)

	if len(offenders) > 0 {
		t.Fatalf(`stream tokens are read outside auth.go at:

    %s

Register the route with handleMedia instead (see auth.go and route_auth_test.go),
so the route table stays the one place that says which routes a stream token
opens.`,
			strings.Join(offenders, "\n    "))
	}
}

// --- AST helpers ---------------------------------------------------------

type apiPackage struct {
	*ast.Package
	fset *token.FileSet
}

func parseAPIPackage(t *testing.T) apiPackage {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	pkg, ok := pkgs["api"]
	if !ok {
		t.Fatal(`package "api" not found in .`)
	}
	return apiPackage{Package: pkg, fset: fset}
}

// isMuxSelector matches `s.mux`.
func isMuxSelector(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "mux"
}

// isMuxRegistration matches `s.mux.HandleFunc(...)` and `s.mux.Handle(...)`.
func isMuxRegistration(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !isMuxSelector(sel.X) {
		return false
	}
	return sel.Sel.Name == "HandleFunc" || sel.Sel.Name == "Handle"
}

// literalPattern is a pattern written out whole. Unlike stringLit it refuses a
// concatenation, whose tail this scan cannot know.
func literalPattern(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok {
		return "", false
	}
	return stringLit(lit)
}
