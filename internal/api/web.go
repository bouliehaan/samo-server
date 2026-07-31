package api

import (
	"embed"
	"fmt"
	"strings"
)

// webFS holds the server's three HTML pages and their stylesheets and scripts.
//
// These used to be Go string constants — a 6,000-line one for the dashboard —
// which cost more than ugliness. A Go raw string cannot contain a backtick, so
// the setup page's JS had to splice them in as ` + "`" + ` wherever it wanted a
// template literal, and no editor, linter or formatter could see any of it as
// the CSS and JavaScript it is.
//
//go:embed web
var webFS embed.FS

// Placeholders the page shells carry where their assets get spliced in. They
// are plain text rather than template actions because these pages have no
// template data at all — see pageSource.
const (
	placeholderBaseCSS = "__SAMO_BASE_CSS__"
	placeholderPageCSS = "__SAMO_PAGE_CSS__"
	placeholderPageJS  = "__SAMO_PAGE_JS__"
)

// webAsset returns an embedded file's contents, panicking if it is missing.
// The only way that happens is a build with a broken go:embed pattern, which
// should fail loudly at startup rather than serve a page with a hole in it.
func webAsset(name string) string {
	data, err := webFS.ReadFile("web/" + name)
	if err != nil {
		panic(fmt.Sprintf("api: embedded web asset %q: %v", name, err))
	}
	return string(data)
}

// pageSource assembles a page shell with its stylesheet and script inlined.
//
// The base stylesheet is shared across all three pages; the page's own CSS and
// JS are named after it. Assembly happens once at startup, not per request.
func pageSource(page string) string {
	return strings.NewReplacer(
		placeholderBaseCSS, webAsset("base.css"),
		placeholderPageCSS, webAsset(page+".css"),
		placeholderPageJS, webAsset(page+".js"),
	).Replace(webAsset(page + ".html"))
}
