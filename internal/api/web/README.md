# internal/api/web

The server's three HTML pages, embedded into the binary by `go:embed` (see
`../web.go`). No CDN, no runtime dependency — the UI is fully styled offline.

| | |
|---|---|
| `base.css` | shared design language, used by all three pages |
| `app.html` / `app.css` / `app.js` | the authenticated dashboard |
| `setup.html` / `setup.css` / `setup.js` | first-run wizard |
| `login.html` / `login.css` / `login.js` | sign-in |

Each `*.html` is a shell carrying three placeholders — `__SAMO_BASE_CSS__`,
`__SAMO_PAGE_CSS__`, `__SAMO_PAGE_JS__` — which `pageSource` fills at startup.

## The design language

The SAMO SERVER console. One monospace face (Office Code Pro), a strictly
NEUTRAL black → grey → white ladder with no blue or steel tint, hard 90-degree
corners, and white used only as a hallmark: active state, focus, the online dot.

It is the terminal of the Samo family — it shares the clients' structure and
restraint, not their cool-grey palette. Palette and tokens are common with the
Android/desktop clients; the all-mono, left-rail console treatment is the
server's own dialect of that system.

## A caveat worth knowing

These pages still go through `html/template`, whose contextual auto-escaping
**strips every CSS and JS comment** on the way out — each one collapses to a
single space. So comments here document the source but never reach the browser,
and adding one to the top of a stylesheet changes the served bytes (`<style> `
instead of `<style>`).

The pages contain no template actions at all, so the template is doing nothing
but that escaping. Replacing it with a real build step that minifies
deliberately is the next step — see `docs/structure-todo.md`.
