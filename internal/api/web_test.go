package api

import (
	"regexp"
	"strings"
	"testing"
)

var uiPages = []string{"app", "setup", "login"}

// Every page shell must have its placeholders filled. A typo in a filename or
// a go:embed pattern would otherwise ship a page with a literal
// __SAMO_SCRIPT__ where its script belongs — which renders fine, looks fine,
// and does nothing.
func TestPageSourceFillsEveryPlaceholder(t *testing.T) {
	for _, page := range uiPages {
		body := pageSource(page)
		if strings.Contains(body, "__SAMO_") {
			t.Errorf("%s: unfilled placeholder remains", page)
		}
		for _, want := range []string{"<!doctype html>", "</html>"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", page, want)
			}
		}
	}
}

var assetRef = regexp.MustCompile(`(?:href|src)="(/assets/build/[^"]+)"`)

// The hashed asset names come from a manifest written by a separate build, so
// a stale manifest or a half-copied build directory leaves the pages pointing
// at files that are not embedded. The browser's only symptom is an unstyled
// page or a dead script, so assert the links resolve.
func TestPagesReferenceAssetsThatExist(t *testing.T) {
	for _, page := range uiPages {
		body := pageSource(page)
		refs := assetRef.FindAllStringSubmatch(body, -1)
		if len(refs) < 2 {
			t.Errorf("%s: expected a stylesheet and a script, found %d asset link(s)", page, len(refs))
			continue
		}
		for _, ref := range refs {
			name := strings.TrimPrefix(ref[1], buildAssetPrefix)
			if _, err := webFS.ReadFile("web/build/" + name); err != nil {
				t.Errorf("%s: references %q which is not embedded: %v", page, ref[1], err)
			}
		}
	}
}

// The bundle uses ES module syntax; a classic <script> would fail on its first
// import.
func TestPagesLoadTheBundleAsAModule(t *testing.T) {
	for _, page := range uiPages {
		if !strings.Contains(pageSource(page), `<script type="module"`) {
			t.Errorf("%s: bundle is not loaded as a module", page)
		}
	}
}

// The pages carry no template data — nothing is passed to Execute. If an
// action ever appears, html/template renders it against nil and silently
// produces an empty string rather than failing, so catch it here instead.
func TestPagesHaveNoTemplateActions(t *testing.T) {
	for _, page := range uiPages {
		if strings.Contains(pageSource(page), "{{") {
			t.Errorf("%s: template action found; pages are static", page)
		}
	}
}

// script-src dropped 'unsafe-inline' when the JS moved into external bundles.
// Re-inlining a script would silently stop working under that policy, so keep
// the two facts wired together.
func TestNoInlineScriptsInPages(t *testing.T) {
	inlineScript := regexp.MustCompile(`<script(?:\s[^>]*)?>[^<\s]`)
	for _, page := range uiPages {
		if inlineScript.MatchString(pageSource(page)) {
			t.Errorf("%s: inline <script> content, but CSP script-src is 'self' only", page)
		}
	}
	if strings.Contains(contentSecurityPolicy, "script-src 'self' 'unsafe-inline'") {
		t.Error("script-src regained 'unsafe-inline'")
	}
}
