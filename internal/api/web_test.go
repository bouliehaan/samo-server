package api

import (
	"strings"
	"testing"
)

// Every page shell must have all three placeholders filled. A typo in a
// filename or a go:embed pattern would otherwise ship a page with a literal
// __SAMO_PAGE_JS__ where its script should be — which renders fine, looks
// fine, and does nothing.
func TestPageSourceFillsEveryPlaceholder(t *testing.T) {
	for _, page := range []string{"app", "setup", "login"} {
		body := pageSource(page)
		if strings.Contains(body, "__SAMO_") {
			t.Errorf("%s: unfilled placeholder remains", page)
		}
		for _, want := range []string{"<!doctype html>", "</html>", "@font-face"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", page, want)
			}
		}
	}
}

// The pages carry no template data — nothing is passed to Execute. If an action
// ever appears, html/template will render it against nil and silently produce
// an empty string rather than fail, so catch it here instead.
func TestPagesHaveNoTemplateActions(t *testing.T) {
	for _, page := range []string{"app", "setup", "login"} {
		if strings.Contains(pageSource(page), "{{") {
			t.Errorf("%s: template action found; pages are static", page)
		}
	}
}
