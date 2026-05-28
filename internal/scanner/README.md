# Scanner

Samo’s music scanner follows [Navidrome’s phased pipeline](https://github.com/navidrome/navidrome/blob/master/scanner/README.md):

| Phase | File | Purpose |
|-------|------|---------|
| 1 | `phase_1_folders.go` | Walk library; import new/changed files (folder-hash incremental scan for music; audiobook/podcast/mixed use legacy walkers) |
| 2 | `phase_2_missing_tracks.go` | Reconcile moved files via persistent track IDs (`track_pid`); cross-library in `phase_2_cross_library.go` |
| 3 + 4 | `phase_3_refresh_albums.go`, `phase_4_playlists.go`, `phase_parallel.go` | Refresh albums then import M3U playlists (music/mixed only; sequential to avoid SQLite write races) |
| Final | `prune.go` | Mark/prune missing files, orphan GC, refresh stats |

Other Navidrome-aligned pieces:

- `.ndignore` — `ignore_checker.go` (walks + filesystem watcher)
- External scan — `external.go` + `samo-server --scan-subprocess` (set `SAMO_SCANNER_EXTERNAL=true`)
- M3U auto-import — `SAMO_AUTO_IMPORT_PLAYLISTS` (default true; requires an admin user)

Entry points:

- `ScanWithProgress` — production scans (libraries service)
- `runLibraryPipeline` — per-library orchestration (`pipeline.go`)

Persistent IDs (Navidrome defaults):

- **Album**: `musicbrainz_albumid|albumartistid,album,albumversion,releasedate` → `music_album_identity.go`
- **Track**: `musicbrainz_trackid|albumid,discnumber,tracknumber,title` → `persistent_id.go`

Folder state is stored in `scan_folders` (see migration `025_scan_folders_track_pid.sql`).
