# Samo Server Architecture

Samo Server is a native self-hosted listening server for music, audiobooks, podcasts, and programmed radio. It is not a wrapper around Navidrome or Audiobookshelf. The goal is to provide Samo-native concepts with enough metadata richness to support a "home Spotify" client experience, while leaving room for compatibility adapters later.

This document is a living guide for humans and AI agents building the server. Update it whenever the architecture, module boundaries, data model, scanner behavior, or API contracts meaningfully change.

## Current State

The server currently has these major pieces:

- HTTP server entrypoint in `cmd/samo-server`
- env-driven process config in `internal/config`
- SQLite open/migrate layer in `internal/storage`
- embedded SQL migrations in `migrations`
- rich catalog domain models and in-memory read service in `internal/catalog`
- SQLite catalog hydration in `internal/catalog/sqlite.go`
- metadata scanners in `internal/scanner`
- optional external metadata lookup providers in `internal/metadata`
- filesystem library watcher in `internal/watch`
- library management and scan orchestration in `internal/libraries`
- embedded cover extraction and cache in `internal/covers`
- playback state persistence in `internal/playback`
- local media file access and streaming in `internal/files`
- remote source ingestion in `internal/sources`
- Samo-native HTTP API handlers in `internal/api`
- 24/7 radio station module in `internal/radio`
- shared media taxonomy in `internal/media`

There is no GUI yet. The current UI surface is only a small root status page. The important product surface is the API.

## Foundation Work Log

Record completed foundation-layer work here so later features do not need to reopen low-level design. Each entry should name the owning package, schema changes, API contracts, and intentional limits.

### 2026-05-22 — Library management, scan jobs, and scanner pruning

**Packages:** `internal/libraries`, `internal/scanner`, `internal/watch`, `internal/api`, `migrations/003_scan_jobs.sql`

**Database**

- Added `scan_jobs` table for scan history: status, scope, library ID, trigger source, timestamps, error text, and prune counters (`files_seen`, `files_pruned`, `items_pruned`).
- Existing `libraries` table remains the source of truth for filesystem roots. Env paths sync into it on startup; API-created libraries use the same deterministic IDs as the scanner (`scanner.LibraryID`).

**`internal/libraries`**

- `SyncConfigured` upserts env libraries without deleting API-managed rows.
- Full CRUD for filesystem libraries (`music` and `shelf` + `book`/`podcast`). Remote `samo://` libraries are read-only through this API.
- `ScanAll` / `ScanLibrary` run under a process-wide mutex, call `scanner.ScanWithStats`, persist a `scan_jobs` row, and return the final job record.
- `ScannerLibraries` lists DB-backed roots for the watcher (skips `samo://` paths).

**`internal/scanner`**

- Per-library scan accumulates seen file paths, shelf item IDs, and podcast episode IDs.
- After each library scan: prune missing `media_files`, shelf items, and local podcast episodes; then prune orphan music tracks/albums/artists.
- `ScanWithStats` returns aggregate counters for job recording. `Scan` delegates to it.

**`internal/watch`**

- Reloads libraries from SQLite before each debounced rescan (no longer tied to startup env snapshot only).

**HTTP API**

- `GET/POST/PATCH/DELETE /api/v1/libraries`
- `POST /api/v1/libraries/{id}/scan`, `POST /api/v1/scan`
- `GET /api/v1/scan/jobs`, `GET /api/v1/scan/jobs/{id}`
- Scan routes refresh the in-memory catalog after success.

**Intentional limits left for later foundation passes**

- Watcher-triggered scans did not yet write `scan_jobs` rows (addressed in the next foundation pass).
- Library path moves that change deterministic IDs need an explicit migration path (addressed in the next foundation pass).
- Playback writes and catalog media streaming were not yet split into dedicated packages (addressed in the next foundation pass below).

### 2026-05-22 — Playback persistence, media streaming, and library hardening

**Packages:** `internal/playback`, `internal/files`, `internal/libraries`, `internal/watch`, `internal/api`

**`internal/playback`**

- Owns all writes to `playback_json` (music artists/albums/tracks/playlists) and `progress_json` (shelf items, podcast episodes).
- Target kinds: `music-artist`, `music-album`, `music-track`, `music-playlist`, `shelf-item`, `shelf-episode`.
- `GET` returns normalized state; `PUT` replaces; `PATCH` merges partial updates with optional play/skip increments and timestamp touches.
- Validates entity existence, rating range (0–5), and non-negative counters before writing.

**`internal/files`**

- Resolves `media_files` rows and serves bytes with `http.ServeContent` (Range requests supported).
- Validates every local path against configured filesystem library roots; rejects paths outside libraries and `samo://` virtual roots.
- Used by track/item/episode stream shortcuts and album/item cover routes.

**`internal/libraries` (hardening)**

- `PATCH` may relocate a library path: inserts the new deterministic ID, moves `library_id` on child rows, deletes the old row.
- `ScanFilesystem` records watcher scans in `scan_jobs` with `trigger_source = filesystem`.

**`internal/watch` (hardening)**

- Uses a `Scan` callback (typically `libraries.ScanFilesystem`) so filesystem rescans share the same mutex, prune logic, and job history as API scans.
- Reloads library roots from SQLite before each debounced rescan.

**HTTP API**

- `GET|PUT|PATCH /api/v1/playback/{kind}/{id}`
- `GET /api/v1/media/files/{id}`, `GET /api/v1/media/files/{id}/stream`
- `GET /api/v1/music/tracks/{id}/stream`, `GET /api/v1/music/albums/{id}/cover`
- `GET /api/v1/shelf/items/{id}/stream`, `GET /api/v1/shelf/items/{id}/cover`, `GET /api/v1/shelf/episodes/{id}/stream`
- Playback and scan routes refresh the in-memory catalog after successful writes.

### 2026-05-22 — Embedded cover extraction and multi-file streaming

**Packages:** `internal/covers`, `internal/scanner`, `internal/files`, `internal/api`, `migrations/004_extracted_covers.sql`

**Database**

- Added `extracted_covers` table mapping stable cover IDs to on-disk cache paths, source audio paths, and source checksums for re-extract decisions.

**`internal/covers`**

- Extracts embedded artwork with `ffmpeg` when sidecar images are absent.
- Caches under `{SAMO_DATA_DIR}/covers/{cover_id}.jpg`.
- Skips re-extraction when the source audio checksum is unchanged and the cache file still exists.
- Detects attached pictures with `ffprobe` before invoking `ffmpeg`.

**`internal/scanner`**

- Computes per-file checksums from path, size, and mtime during probe.
- Resolves covers in priority order: sidecar image in folder, then embedded extraction.
- `Scanner` accepts `Options{Covers}` via `NewWithOptions`.

**`internal/files`**

- Allows serving from the cover cache directory in addition to library roots.

**HTTP API**

- `GET /api/v1/media/covers/{id}` (metadata) and `GET /api/v1/media/covers/{id}/image`
- Track/item/episode stream shortcuts accept `?mediaFileId=` to choose a specific linked audio file

### 2026-05-22 — Podcast feed polling and refresh scheduling

**Packages:** `internal/sources`, `internal/config`, `internal/api`, `cmd/samo-server`, `migrations/005_podcast_poll.sql`

**Database**

- Added poll columns on `podcast_feeds`: `poll_enabled`, `poll_interval_seconds`, `next_poll_at`, `last_poll_started_at`, `last_poll_finished_at`, `consecutive_errors`.
- Index on `(poll_enabled, next_poll_at)` for due-feed queries.

**`internal/sources`**

- Each feed carries a `Poll` schedule (enabled flag, interval, next run, last start/finish, consecutive errors).
- `UpdatePodcastFeed` patches title and poll settings without re-fetching RSS.
- `ListDuePodcastFeeds` / `RunPodcastPollCycle` refresh feeds whose `next_poll_at` is due.
- Manual `RefreshPodcastFeed` and scheduled polls share the same path: mark start, fetch/save RSS, mark success with interval-based `next_poll_at`, or mark failure with capped exponential backoff (15m floor, 6h ceiling).
- `savePodcastFeed` upserts feed metadata on refresh without overwriting user poll settings.
- `Poller` runs on a ticker (`SAMO_PODCAST_POLL_TICK`, default 1m) and reloads the in-memory catalog after successful updates.

**HTTP API**

- `PATCH /api/v1/shelf/podcast-feeds/{id}` — update title, `pollEnabled`, `pollIntervalSeconds` (900s–7d).
- `POST /api/v1/shelf/podcast-feeds/poll` — run one poll cycle immediately (admin-style trigger).

**Config**

- `SAMO_PODCAST_POLL` (default `true`) — start background poller.
- `SAMO_PODCAST_POLL_TICK` (default `1m`) — scheduler tick interval.

## Startup Flow

`cmd/samo-server/main.go` owns process assembly only. Keep it thin.

Startup currently does this:

1. Load env config with `config.LoadEnv`.
2. Open SQLite with `storage.Open`.
3. Apply embedded migrations with `storage.ApplyMigrations`.
4. Create `covers.Service` (cache dir under `{SAMO_DATA_DIR}/covers`).
5. Create `scanner.Scanner` with the covers service wired in, then `libraries.Service`.
6. Sync env-configured libraries into SQLite with `libraries.Service.SyncConfigured`.
7. If configured, scan libraries on startup with `libraries.Service.ScanAll`.
8. Hydrate catalog seed data from SQLite with `catalog.LoadSeedFromDB`.
9. Load optional radio JSON config with `radio.LoadConfigFile`.
10. Create `catalog.Service`, `playback.Service`, `files.Service`, `metadata.Service`, `radio.Service`, and `sources.Service`.
11. Build `api.Server`.
12. If configured, start the podcast feed poller (reloads catalog after successful poll cycles).
13. If configured, start the library watcher (reloads libraries from SQLite, records filesystem scan jobs).
14. Start `http.ListenAndServe`.

This means HTTP handlers do not talk directly to scanner code or raw startup config. They receive services.

## Configuration

Main environment variables:

- `SAMO_ADDR`: listen address, defaults to `:4500`
- `SAMO_DATA_DIR`: data directory, defaults to `data`
- `SAMO_DB_PATH`: SQLite database path, defaults to `data/samo.db`
- `SAMO_RADIO_CONFIG`: radio JSON config, defaults to `data/radio.json`
- `SAMO_API_TOKEN`: optional bearer token for `/api/v1/*`
- `SAMO_SCAN_ON_START`: defaults to `true`
- `SAMO_WATCH_LIBRARIES`: defaults to `true`
- `SAMO_WATCH_DEBOUNCE`: filesystem change debounce duration, defaults to `3s`
- `SAMO_METADATA_PROVIDERS`: comma-separated external lookup providers, defaults to empty/off
- `SAMO_METADATA_USER_AGENT`: user-agent for external metadata requests, used by MusicBrainz
- `SAMO_MUSIC_DIRS`: path-list of music library folders
- `SAMO_AUDIOBOOK_DIRS`: path-list of audiobook library folders
- `SAMO_PODCAST_DIRS`: path-list of podcast library folders
- `SAMO_PODCAST_POLL`: enable background RSS polling, defaults to `true`
- `SAMO_PODCAST_POLL_TICK`: poll scheduler tick interval, defaults to `1m`

Embedded cover art is cached under `{SAMO_DATA_DIR}/covers`. Static `ffmpeg` and `ffprobe` ship in `bin/` beside the server binary on Ubuntu (see `internal/toolchain` and [docs/install-ubuntu.md](docs/install-ubuntu.md)).

Path-list values use the OS separator, so Linux/macOS use `:`.

## Package Responsibilities

### `cmd/samo-server`

Process composition only. It should not contain business logic, SQL details, scanner parsing, route behavior, or metadata mapping.

### `internal/config`

Loads and validates process-level settings from env vars. Feature-specific config belongs near the owning module when it grows beyond simple env fields.

### `internal/toolchain`

Resolves bundled `ffmpeg` and `ffprobe` for Ubuntu/Linux deployments.

Lookup order:

1. `SAMO_FFMPEG_PATH` / `SAMO_FFPROBE_PATH` overrides
2. `{executable_dir}/bin/ffmpeg` and `bin/ffprobe` (release tarball layout)
3. `{SAMO_DATA_DIR}/tools/linux-gpl-latest/{linux-amd64|linux-arm64}/` cached extract
4. repository `bin/` during local development
5. embedded assets when built with `-tags bundled`

Samo does not search the system `PATH` for ffmpeg. Ubuntu servers should use the bundled `bin/` layout from [docs/install-ubuntu.md](docs/install-ubuntu.md).

### `internal/storage`

Owns SQLite connection setup and migration application.

Responsibilities:

- create database parent directory
- open `sqlite3`
- apply pragmas
- run migrations idempotently

It should not know about music, audiobooks, radio, or HTTP.

### `migrations`

Embeds ordered `.sql` migrations. Migration names are the migration versions.

Current schema stores:

- libraries
- scan jobs (library scan history and prune counters)
- media files
- music artists, albums, tracks, playlists, genres
- music artist/album/track relationships
- shelf libraries/items/authors/series/chapters
- podcast episodes
- remote podcast feed source records
- internet radio station source records
- JSON columns for rich metadata that is still evolving

Use migrations for schema evolution. Do not silently mutate schema in Go code.

### `internal/catalog`

Defines Samo-native catalog domain/API DTOs and read behavior.

Important files:

- `types.go`: shared metadata structs like images, external IDs, audio files, chapters, playback state
- `music_types.go`: artists, albums, tracks, playlists, music search
- `shelf_types.go`: libraries, audiobook/podcast items, authors, series, podcast episodes
- `service.go`: in-memory read service used by API handlers
- `sqlite.go`: hydrates a `catalog.Seed` from SQLite

The catalog service is read-optimized and guarded by a mutex. It can be refreshed with `Replace(seed)` after scans. Handlers continue to read through the service rather than querying raw SQL.

### `internal/metadata`

Owns optional external metadata lookup providers. This package is for explicit user-initiated search only, like a future web UI asking "find metadata for this book/show/album." It must not run automatically during scanner walks, watcher events, source ingestion, or startup.

Current providers:

- `openlibrary`: audiobook/book metadata through Open Library search
- `googlebooks`: audiobook/book metadata through Google Books volume search
- `itunes`: podcast directory metadata through Apple's iTunes Search API
- `musicbrainz`: music artist, album/release-group, and track/recording metadata through MusicBrainz search

Providers are disabled by default. `cmd/samo-server` only registers providers named in `SAMO_METADATA_PROVIDERS`. An empty provider list means `/api/v1/metadata/search` returns no results and performs no outbound network calls.

Responsibilities:

- define common metadata search request/result DTOs
- normalize provider configuration
- route explicit searches to enabled providers
- map provider-specific JSON into Samo metadata candidates
- preserve provider IDs and URLs in `catalog.ExternalIDs`/links

It should not write catalog rows directly. A later UI/apply workflow can decide which candidate fields to merge into catalog items.

### `internal/libraries`

Owns filesystem library records, scan orchestration, and scan job history.

Responsibilities:

- sync env-configured library paths into SQLite on startup
- list/create/update/delete filesystem libraries over the API
- trigger full or per-library scans
- record scan job status, timing, and prune counts
- expose the current scanner library list for the filesystem watcher

It should not parse tags, call `ffprobe`, or handle HTTP directly.

### `internal/covers`

Owns embedded artwork extraction and the on-disk cover cache.

Responsibilities:

- detect attached pictures in audio files
- extract artwork into `{dataDir}/covers`
- persist `extracted_covers` rows with source checksums
- serve stable cover IDs to catalog image metadata

It should not scan full libraries or handle HTTP directly.

### `internal/playback`

Owns listening-state writes for catalog entities stored as JSON in SQLite.

Responsibilities:

- read and write `playback_json` / `progress_json` columns
- validate target kind and ID existence
- normalize and validate playback payloads (rating, counts, progress)
- support explicit PUT replacement and PATCH merges

It should not hydrate the catalog read model directly; callers refresh `catalog.Service` after writes.

### `internal/files`

Owns safe access to on-disk media referenced by the catalog.

Responsibilities:

- load `media_files` metadata by ID
- verify local paths stay inside configured filesystem library roots
- stream audio/image bytes with range support and content types from DB metadata

It should not scan libraries, parse tags, or own playback state.

### `internal/scanner`

Walks configured libraries, calls `ffprobe`, maps discovered metadata into SQLite.

Current scanner behavior:

- accepts common audio extensions
- skips hidden directories
- uses deterministic SHA-256 based IDs
- uses `ffprobe` for container, stream, tag, chapter, and duration metadata
- creates music artists/albums/tracks from tags
- groups audiobooks by folder
- groups podcasts by first folder under the podcast library
- writes technical audio file metadata
- tracks detected embedded metadata formats such as ID3, MP4, and Vorbis comments
- writes embedded tags as JSON
- writes audiobook chapters from embedded chapter data or file-part fallback chapters
- prunes stale media files, shelf items, and local podcast episodes after each library scan
- removes orphaned music tracks, albums, and artists after pruning
- reads OverDrive MediaMarkers into audiobook chapters when available
- reads audiobook sidecars: `desc.txt`, `reader.txt`, and `.opf`
- discovers local cover images such as `cover.jpg`, `folder.png`, `front.webp`, or `album.jpg`
- extracts embedded cover art into the cover cache when sidecars are missing

The scanner writes to SQLite. It should not return API response DTOs directly to HTTP.

### `internal/watch`

Owns filesystem watching and scan orchestration.

Responsibilities:

- recursively watch configured library roots
- add watches for newly-created directories
- filter events to audio, sidecar metadata, and cover image paths
- debounce bursts of writes
- reload library roots from SQLite before each rescan
- call the shared library scan callback (records `scan_jobs`, prunes stale rows)
- run scanner after changes settle
- reload catalog seed data from SQLite
- call `catalog.Service.Replace` so API handlers see new data

It should not parse metadata, write SQL directly, or handle HTTP.

### `internal/sources`

Owns user-added remote sources. This package is the first "coagulator" layer: it pulls remote inputs into Samo-owned catalog data without making handlers or scanners know remote feed details.

Current responsibilities:

- validate and fetch podcast RSS feed URLs
- parse RSS and iTunes podcast metadata
- upsert remote podcast feeds as shelf podcast items and podcast episodes
- store podcast feed source state, fetch timestamps, errors, and poll schedule
- poll due RSS feeds on a background ticker with backoff on failure
- store user-added internet radio station URLs
- expose source read/write methods for API handlers

Podcast feeds are unique because a podcast can come from local files through `internal/scanner` or from RSS through `internal/sources`. Both paths write into the same shelf podcast and episode tables so clients can use one podcast API shape.

Internet radio stations are simpler source records: Samo stores the stream URL and descriptive metadata, then exposes public playlist/redirect links. They do not create shelf items and they do not participate in the 24/7 scheduler yet.

`internal/sources` may write SQL because it owns these source ingestion workflows. It should not handle HTTP and should not become a general catalog query service.

### `internal/api`

Owns HTTP routing and response writing.

Current namespaces:

- `/api/v1/libraries/*`
- `/api/v1/scan/*`
- `/api/v1/playback/*`
- `/api/v1/media/*`
- `/api/v1/catalog/*`
- `/api/v1/metadata/*`
- `/api/v1/music/*`
- `/api/v1/shelf/*`
- `/api/v1/radio/*`
- `/api/v1/internet-radio/*`
- public `/radio/{id}/playlist.m3u`
- public `/radio/{id}/stream`
- public `/internet-radio/{id}/playlist.m3u`
- public `/internet-radio/{id}/stream`

API handlers should stay thin:

- parse path/query/body
- call services
- map errors to HTTP responses
- write JSON or stream output

Do not put SQL, scanning, tag parsing, or long-running workflow logic in handlers.

### `internal/radio`

Owns the first radio module:

- loads optional JSON station config
- normalizes stations and media items
- calculates deterministic current/upcoming slots
- exposes stream behavior from scheduled local files

The current stream is first-generation: it seeks approximately by duration and throttles file bytes to approximate real-time playback. It assumes compatible source files per station.

### `internal/media`

Shared media taxonomy used across radio/catalog/scanner. Keep broad media kind definitions here when they are shared by multiple modules.

## API Direction

Samo's primary API is native and metadata-rich. Compatibility layers can come later.

The native API is documented in `docs/api.md`.

Current music surface:

- artists
- albums
- tracks
- genres
- playlists
- search

Current shelf surface:

- libraries
- items
- audiobooks
- authors
- series
- podcasts
- episodes
- search

Current radio surface:

- stations
- station detail
- now playing
- schedule
- playlist
- stream

Current source-ingestion surface:

- podcast feed list/create/detail/refresh/delete
- internet radio station list/create/detail/delete
- public M3U and stream redirect routes for internet radio stations

Current library management surface:

- filesystem library list/create/update/delete
- manual full-library and per-library scan triggers
- scan job list/detail with prune counts

Current playback surface:

- get, replace, and patch playback/progress state for music and shelf entities
- optional play/skip increments and last-played timestamps on patch

Current media surface:

- media file metadata by ID
- byte streaming for media files, tracks, shelf items, and podcast episodes
- cover image streaming for music albums and shelf items with local cover paths

Current metadata lookup surface:

- enabled metadata provider list
- explicit metadata search for audiobooks, podcasts, and music
- no write/apply endpoint yet

The API should preserve rich metadata rather than flattening to the lowest common denominator. Navidrome/OpenSubsonic and Audiobookshelf compatibility should map into or out of Samo-native models.

## Metadata Philosophy

Samo clients are expected to use rich metadata. Capture and preserve more than the first UI needs.

Important metadata categories:

- canonical IDs and provider IDs
- sort titles and sort names
- display artist strings alongside structured artist arrays
- music technical metadata
- music release version, compilation flag, label, catalog number, barcode, MusicBrainz/Discogs/Spotify/Apple/ISRC IDs
- audiobook narrators, authors, series, ISBN/ASIN/provider IDs
- audiobook sidecars and OverDrive MediaMarkers
- podcast feed, owner, episode, enclosure, and GUID data
- embedded tags
- chapters
- cover/image metadata
- playback/progress/rating/favorite state

When unsure whether a field matters, prefer storing it in a structured place or in embedded tags rather than discarding it.

## Architecture Rules

Avoid god components. Keep ownership boundaries clear.

Rules:

- `cmd` wires dependencies; it does not implement behavior.
- `api` handles HTTP only.
- `catalog` defines read models and catalog query behavior.
- `playback` owns listening-state writes.
- `files` owns validated local media byte serving.
- `metadata` searches external metadata providers only when explicitly called.
- `libraries` owns library records and scan orchestration.
- `covers` owns embedded artwork extraction and cache rows.
- `scanner` discovers and writes metadata.
- `watch` observes filesystem changes and coordinates scanner/catalog refresh.
- `sources` ingests remote RSS and stream URL records.
- `storage` owns DB setup/migration mechanics.
- `radio` owns radio scheduling/streaming behavior.
- shared types only move to shared packages after multiple modules need them.
- avoid cross-package cycles.
- prefer interfaces only when they remove real coupling or unlock tests.
- keep SQL out of HTTP handlers.
- keep ffprobe/tag parsing out of HTTP handlers and catalog read service.
- add migrations for schema changes.
- add focused tests around package boundaries.

## Current Known Limits

These are expected first-pass limits, not accidental bugs:

- scanner does not fetch remote metadata; remote RSS ingestion lives in `internal/sources`
- external metadata lookup is search-only; it does not apply changes to catalog items yet
- scanner does not download remote cover art (only sidecars + embedded extraction)
- scanner does not transcode audio
- relocating a library path assigns a new deterministic library ID; clients must follow the ID returned by `PATCH`
- stream shortcuts default to the first linked `media_files` row; use `?mediaFileId=` for others
- radio config is still JSON-backed rather than catalog-backed
- internet radio stations are stored and shared, but not probed for live metadata yet
- podcast feed deletion removes the remote podcast shelf item; it does not reconcile with a local-file podcast of the same show yet
- auth is token-gated API access, not full users/sessions yet

## Near-Term Build Path

Useful next steps:

1. Expand scanner metadata extraction to cover the full Samo client needs.
2. Add disc-aware default stream selection for multi-file albums and audiobooks.
3. Add metadata candidate apply/merge workflows with user confirmation.
4. Add Subsonic/OpenSubsonic compatibility routes.
5. Add Audiobookshelf compatibility routes.
6. Make radio stations optionally source from catalog queries/playlists.

## Testing Expectations

Current tests cover:

- API route/token behavior
- rich seeded API response metadata
- catalog filtering/search
- SQLite migration idempotency
- SQLite hydration into catalog seed
- ffprobe result normalization
- audiobook grouping
- external metadata provider mapping
- radio deterministic schedule behavior
- library CRUD and scan job recording
- scanner prune behavior for stale files and orphan music rows
- playback PUT/PATCH validation and persistence
- local path sandboxing and media file range streaming
- library path relocation and filesystem scan job recording
- embedded cover extraction, cache rows, and cover ID serving
- audio file checksum computation and `mediaFileId` stream selection
- podcast feed poll scheduling, due-feed selection, and refresh without clobbering poll settings

Keep tests focused on contracts and package boundaries. Scanner tests should prefer parser/grouping tests and small generated media fixtures when practical.
