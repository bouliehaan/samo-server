# Structural work still outstanding

Working notes, not a spec. Delete when it stops being useful.

Context: commit `aa64011` landed reliability hardening, leveled logging, the
catalog model/store split, and the Subsonic adapter. The items below are what
that pass did **not** finish, in priority order.

---

## 1. Scanner: extract persistence (the big one)

**State:** `internal/scanner` holds **104 SQL statements across 15 files**. Every
other package is either clean or already isolates its SQL in one file. This is
the last real structural mess.

**Target shape** (proven on `catalog` in `aa64011`):

```
scanner  decides what an entity is, applies override policy, accounts for
         what it saw
store    runs the statement, returns what the database did
```

So an upsert becomes three readable steps instead of one fused blob:

```go
func (s *Scanner) upsertMusicArtist(ctx context.Context, artist catalog.MusicArtist) error {
    if s.overrideIndex != nil {                       // policy
        artist, err = catalogstore.GuardMusicArtist(ctx, s.db, s.overrideIndex, artist)
    }
    created, err := s.store.UpsertMusicArtist(ctx, artist)   // persistence
    if s.activeScan != nil && created {                      // accounting
        s.activeScan.noteNewArtist(artist.ID)
    }
}
```

The store returns *facts* (`created bool`) rather than doing scan bookkeeping,
so it holds no scan state.

**Where the SQL is:**

| file | stmts | |
|---|---|---|
| `write.go` | 44 | 10 pure, 10 open with a uniform override-guard block, 3 special |
| `prune.go` | 18 | `pruneOrphanMusic` alone is 8 and fully pure |
| `media_file_relink.go` | 7 | |
| `media_file_reclaim.go` | 6 | mostly pure |
| `phase_2_missing_tracks.go` | 5 | `moveMatchedTrack` calls `noteTrackIDMigration` — mixed |
| `phase_2_cross_library.go` | 5 | the three `findRecentBy*` are pure |
| `folder_store.go` | 4 | `saveFolderHash` needs `albumFolder.hash()` — keep in scanner |
| `audiobook_audio_align.go` | 4 | reaches into ffprobe/analysis types — mostly stays |
| rest | 11 | 1–3 each |

**Genuinely mixed (extract only the query, leave orchestration):**
`healAudioDerivedChapters`, `reconcileMediaFileTrackLinks`,
`reconcileAudiobookMediaOwners`, `reconcilePodcastMediaOwners`,
`refreshMusicAlbumsForLibrary`, `runPhaseCrossLibraryMoves`,
`markUnseenMediaFilesMissing`, `moveMatchedTrack`,
`reconcilePlaylistTrackReferences`, `scanLibraryWithStats`, `pruneMediaFiles`,
`upsertAudioFile`.

### How to do it — and how NOT to

This was attempted twice and reverted twice. Both failures had the same cause:
**bulk regex transformations.**

**Do not:**
- Bulk-rename struct fields. `path`→`Path` silently rewrote `excluded.path`
  inside SQL string literals (20 of them), `os.FileInfo.ModTime()`, and an
  unrelated `pendingDir.path`.
- Move a whole file. Coupling is per-method, not per-file.
- Move a type that has methods on it. Go forbids defining methods on another
  package's type, and it always surfaces late. `albumFolder` (has `hash()`) and
  `catalog.OverrideIndex` both hit this.
- Trust a "purity" check that only inspects struct fields. It misses method
  calls back into scanner state (`noteTrackIDMigration`, `fileInode`).

**Do:**
- One method at a time. Move it, `go build`, `go test ./internal/scanner/...`,
  commit. Roughly 40 iterations. Boring is the point.
- Keep types that carry behaviour in the scanner; pass primitives to the store
  instead (`UpsertLibrary(ctx, id, name, kind, mediaType, path)` rather than
  taking `scanner.Library`).
- Row shapes with only data (`indexedMediaFile`, `mediaFileOwnerSnapshot`) can
  move; give them exported fields *by editing the struct*, not by regex.

**Verify with more than tests.** The scanner is the write path. After any
change: scan a library, pin an album title and an artist name via
`POST /api/v1/metadata/apply`, then run a **full rescan** and confirm both
survive. That is the override-guard policy; breaking it silently wipes manual
metadata edits on every scan and would not show up for weeks. The `/verify`
skill boots a real server against the test Postgres.

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
| model/persistence split | catalog only | everywhere |
| SQL in service packages | scanner (104) + 6 small | none |
| frontend | 6k-line Go string | React, built separately |
| live updates | polling | SSE |

Deliberately *not* copying: repository interfaces. Navidrome needs
`model.DataStore` for mock-based unit tests and historical multi-backend
support. Samo is Postgres-only by choice and its test infra clones a real
migrated database per test, which catches SQL bugs mocks structurally cannot.
Adding interfaces would match their shape while losing the better property.
