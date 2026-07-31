# Structural work still outstanding

Working notes, not a spec. Delete when it stops being useful.

Context: commit `aa64011` landed reliability hardening, leveled logging, the
catalog model/store split, and the Subsonic adapter. The items below are what
that pass did **not** finish, in priority order.

---

## 1. ~~Scanner: extract persistence~~ — **done**

`internal/scanner` now contains **zero SQL**. It decides what an entity is,
applies override policy, and accounts for what it saw; `internal/scannerstore`
runs the statements and reports what the database did.

Landed across `ce52da9`, `ef248ec`, `640e057`, `75c4d63`, `c1a4645`, `3482d5a`.
`write.go` went 895 → 328 lines, `prune.go` 508 → 379.

**What made it work this time.** A read-only checker, not a smarter
transformation: extract every backtick SQL literal from a git rev and from the
working tree, normalize whitespace, and diff the sets. Run it after every move.
It caught a mangled `ON CONFLICT` clause within minutes of the first extraction,
and at the end proved that all ~100 statements were byte-identical apart from
three deliberate changes. The prior two attempts failed to bulk regex edits;
the fix was not "regex more carefully" but "make it cheap to prove nothing
changed."

Three things the extraction turned up that were worth fixing on their own:

- `pruneOrphanMusic` picked its JSON syntax by reflecting on the driver's Go
  type name, and the SQLite branch it could fall back to was unreachable *and*
  the default. Removed.
- The orphan-prune JSON cast had no empty-string guard, so one malformed
  playlist row would fail every library's prune. Guarded, with a test that
  fails without it.
- Three statements were duplicated verbatim across two call sites each
  (album-artist names, chapter source, episode delete). Now one each.

`storage.IsUniqueViolation` (pgconn 23505) replaced matching `err.Error()` for
the substring `"unique"` in the scanner. **`internal/libraries/db.go` still has
three of those** — worth the same treatment.

**Verified beyond tests**, as this doc previously insisted: a real server
against the test Postgres, scan → pin an album title and artist name via
`POST /api/v1/metadata/apply` → full rescan. Both survived. The control that
makes that meaningful: deleting the album override and rescanning reverted the
title to the tag-derived value, proving the scan really does rewrite that
column and the guard is what held it. The `/verify` skill boots this in a
couple of minutes.

---

## 2. ~~Web UI: extract to a real frontend~~ — **done**

Source is `web/src`, an npm project built by Vite into `internal/api/web/build`
and embedded with `go:embed`. The Go side of the three pages is 129 lines where
it was 6,981. Landed across `47f247d`, `73c9e47`, `c91ea55`.

The move was done in two provable steps rather than one. First the CSS and JS
came out of the Go constants verbatim, checked by diffing the rendered bytes of
all three pages against a capture taken beforehand — byte-identical. Only then
did the build step go in, so anything that broke afterwards was the bundler,
not the extraction.

Three things it turned up:

- **`html/template` was stripping every CSS and JS comment** — 208 KB of them
  from `app.js` alone — as a side effect of contextual escaping. Nobody chose
  that. The pages contain zero template actions, so the template was doing
  nothing else; Vite now minifies deliberately (227 KB → 137 KB, 33 KB gzipped,
  and cached immutably instead of re-sent inside every page load).
- **The CSP could drop `script-src 'unsafe-inline'`.** The comment on it said
  as much and called it a follow-up. With the JS in external bundles and no
  inline handlers anywhere, it is now `script-src 'self'`. `style-src` still
  needs it: 43 inline `style=""` attributes are written through `innerHTML`,
  and `style-src` governs attributes too. Moving those to custom properties
  closes the last of it.
- **A Go raw string cannot contain a backtick**, so `setup_page.go` had spliced
  them in as `` ` + "`" + ` `` at four sites just to write a template literal.

**ESLint's `no-undef` is the load-bearing part**, not a nicety. In one IIFE
every function saw every other by sharing a scope; in modules, a call to
something you forgot to import is a reference to an undefined global, which
Rollup bundles silently and which throws the first time that path runs. `make
ui` lints before it builds. It caught 191 real errors on its first run — see
`c91ea55` for the two ways a "purity" check gets this wrong.

**59 of 190 functions are modularized** so far, the ones that touch no
module-level binding at all. The remaining ~130 share 33 mutable bindings and
38 consts (cached DOM handles, lookup maps), so they need state ownership
decided per group — real work, not a mechanical move. Do it a group at a time,
lint after each.

`build/` is committed: `go:embed` is a compile-time dependency, so an absent
build directory is a broken `go build`, not a stale UI. `make ui-check` fails
if it has drifted from `web/src`; the Docker image rebuilds it in its own Node
stage rather than trusting the commit.

### Still polling

The UI runs `setInterval(tick, 1500/2000)`, so every open tab is constant load.
SSE is the fix and is now much easier to do — the client code is real modules
with a lint that catches mistakes — but it is a behaviour change with its own
design (endpoint, reconnect, backpressure), not part of the extraction.

---

## 3. Smaller, genuinely optional

- **In-memory catalog ceiling.** Measured: ~3.1 KB/track — 30 MB at 10k, 148 MB
  at 50k, 592 MB at 200k. Indexed SQL beats the in-memory linear scan above
  ~15k tracks (10× faster at 100k, because `MusicTracksForAlbum` is O(n)). The
  reload freeze is fixed, so this is now a deliberate scaling decision, not a
  bug. Hybrid is the answer if you want Pi-class hardware: keep artists/albums
  in RAM, query tracks from SQL.
- **Coverage gaps:** `internal/api` 27% (largest surface, thinnest net),
  `watch` 6%, `artistmeta` 0%.
- **Subsonic:** `getStarred`/`getStarred2` return empty collections; starring is
  a native concept not yet projected. Deliberate — several clients abort their
  whole sync on an error.
- **Packages that already isolate SQL** and need nothing: `lastfm`, `channels`,
  `radio`, `users`, `artistimages`. `internal/api` has zero SQL.

---

## What "Navidrome level" still means here

| | samo | Navidrome |
|---|---|---|
| model/persistence split | catalog + scanner | everywhere |
| SQL in service packages | 6 small packages, each isolating it in one file | none |
| frontend | Vite build, 6 modules + a 3.7k-line core | React, built separately |
| live updates | polling | SSE |

Polling is now the only row where samo is plainly behind. The frontend gap is
narrower than the table suggests — the build, the lint and the module boundary
exist; what remains is continuing to carve up `app.js`, which is ordinary work
rather than a missing capability.

Deliberately *not* copying: repository interfaces. Navidrome needs
`model.DataStore` for mock-based unit tests and historical multi-backend
support. Samo is Postgres-only by choice and its test infra clones a real
migrated database per test, which catches SQL bugs mocks structurally cannot.
Adding interfaces would match their shape while losing the better property.
