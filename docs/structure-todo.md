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

## 2. Web UI: extract to a real frontend

**State:** `internal/api/app_page.go` is **6,007 lines in a single Go function**
— HTML, CSS and ~900 lines of JavaScript in a string literal. No build step, no
linting, no type checking, no tests. Plus `setup_page.go` (794) and
`login_page.go` (180).

**Approved approach:** Vite + `go:embed`. Build-time dependency only — Vite
emits static files, `go:embed` bundles them, and the shipped artifact stays a
single static Go binary plus ffmpeg. Runtime weight unchanged; only dev
machines and CI need Node.

The UI also polls (`setInterval(tick, 1500/2000)`) rather than using SSE, so
every open tab is constant load. Worth fixing in the same pass.

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
| frontend | 6k-line Go string | React, built separately |
| live updates | polling | SSE |

The frontend is now the largest structural gap, and the only remaining one that
a reader would notice immediately.

Deliberately *not* copying: repository interfaces. Navidrome needs
`model.DataStore` for mock-based unit tests and historical multi-backend
support. Samo is Postgres-only by choice and its test infra clones a real
migrated database per test, which catches SQL bugs mocks structurally cannot.
Adding interfaces would match their shape while losing the better property.
