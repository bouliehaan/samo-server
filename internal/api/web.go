package api

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
)

// webFS holds the three page shells and the bundle Vite builds from web/src.
//
// The pages used to be Go string constants — a 6,000-line one for the
// dashboard — which cost more than ugliness. A Go raw string cannot contain a
// backtick, so the setup page's JS spliced them in as ` + "`" + ` wherever it
// wanted a template literal, and nothing could see 260 KB of CSS and
// JavaScript as anything but one opaque string.
//
// build/ is generated (`make ui`) and committed, so `go build` works from a
// clean checkout with only Go installed. That matters more here than keeping
// generated files out of git: go:embed is a compile-time dependency, so an
// absent build/ is not a stale UI, it is a build failure.
//
//go:embed web/*.html web/build
var webFS embed.FS

// Placeholders the page shells carry where their assets get linked in. They
// are plain text rather than template actions because these pages have no
// template data at all — see pageSource.
const (
	placeholderStyles = "__SAMO_STYLES__"
	placeholderScript = "__SAMO_SCRIPT__"
)

// buildAssetPrefix is where the hashed bundle is served from. Every file under
// it is content-addressed, so it is cached immutably.
const buildAssetPrefix = "/assets/build/"

// viteManifest is the subset of Vite's manifest.json this needs: for each
// entry, the built script and the stylesheets that belong to it.
type viteManifest map[string]struct {
	File    string   `json:"file"`
	IsEntry bool     `json:"isEntry"`
	CSS     []string `json:"css"`
}

var manifest = loadManifest()

// loadManifest reads the build manifest once at startup. A missing or
// unparseable manifest means the bundle was never built or is corrupt, which
// should stop the server rather than serve three pages with no styles or
// behaviour at all.
func loadManifest() viteManifest {
	data, err := webFS.ReadFile("web/build/manifest.json")
	if err != nil {
		panic(fmt.Sprintf("api: web build manifest missing (run `make ui`): %v", err))
	}
	var m viteManifest
	if err := json.Unmarshal(data, &m); err != nil {
		panic(fmt.Sprintf("api: web build manifest is not valid JSON: %v", err))
	}
	return m
}

// pageSource assembles a page shell with links to its built assets.
//
// Assembly happens once at startup, not per request. The stylesheet order
// within a page does not need sorting for correctness — base.css is @imported
// at the top of each page's stylesheet, so one file per entry comes out — but
// the sort keeps the output stable if that ever changes.
func pageSource(page string) string {
	entry, ok := manifest["src/"+page+".js"]
	if !ok || !entry.IsEntry {
		panic(fmt.Sprintf("api: no build entry for page %q (run `make ui`)", page))
	}

	styles := append([]string(nil), entry.CSS...)
	sort.Strings(styles)
	var head strings.Builder
	for _, css := range styles {
		fmt.Fprintf(&head, `<link rel="stylesheet" href="%s%s">`, buildAssetPrefix, css)
	}
	script := fmt.Sprintf(`<script type="module" src="%s%s"></script>`, buildAssetPrefix, entry.File)

	shell, err := webFS.ReadFile("web/" + page + ".html")
	if err != nil {
		panic(fmt.Sprintf("api: page shell %q: %v", page, err))
	}
	return strings.NewReplacer(
		placeholderStyles, head.String(),
		placeholderScript, script,
	).Replace(string(shell))
}

// serveBuildAssets serves the hashed bundle. Filenames carry a content hash,
// so a given URL's bytes never change and the response is immutable — the
// browser re-fetches only when a rebuild changes the name in the page shell.
func serveBuildAssets() http.Handler {
	sub, err := fs.Sub(webFS, "web/build")
	if err != nil {
		panic(fmt.Sprintf("api: web build assets: %v", err))
	}
	files := http.FileServer(http.FS(sub))
	return http.StripPrefix(buildAssetPrefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		files.ServeHTTP(w, r)
	}))
}
