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
- remote source ingestion in `internal/sources`
- Samo-native HTTP API handlers in `internal/api`
- 24/7 radio station module in `internal/radio`
- shared media taxonomy in `internal/media`

There is no GUI yet. The current UI surface is only a small root status page. The important product surface is the API.

## Startup Flow

`cmd/samo-server/main.go` owns process assembly only. Keep it thin.

Startup currently does this:

1. Load env config with `config.LoadEnv`.
2. Open SQLite with `storage.Open`.
3. Apply embedded migrations with `storage.ApplyMigrations`.
4. If configured, scan library folders on startup with `scanner.New(db).Scan`.
5. Hydrate catalog seed data from SQLite with `catalog.LoadSeedFromDB`.
6. Load optional radio JSON config with `radio.LoadConfigFile`.
7. Create `catalog.Service`, `metadata.Service`, `radio.Service`, and `sources.Service`.
8. Build `api.Server`.
9. If configured, start the library watcher.
10. Start `http.ListenAndServe`.

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

Path-list values use the OS separator, so Linux/macOS use `:`.

## Package Responsibilities

### `cmd/samo-server`

Process composition only. It should not contain business logic, SQL details, scanner parsing, route behavior, or metadata mapping.

### `internal/config`

Loads and validates process-level settings from env vars. Feature-specific config belongs near the owning module when it grows beyond simple env fields.

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
- reads OverDrive MediaMarkers into audiobook chapters when available
- reads audiobook sidecars: `desc.txt`, `reader.txt`, and `.opf`
- discovers local cover images such as `cover.jpg`, `folder.png`, `front.webp`, or `album.jpg`

The scanner writes to SQLite. It should not return API response DTOs directly to HTTP.

### `internal/watch`

Owns filesystem watching and scan orchestration.

Responsibilities:

- recursively watch configured library roots
- add watches for newly-created directories
- filter events to audio, sidecar metadata, and cover image paths
- debounce bursts of writes
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
- store podcast feed source state, fetch timestamps, and errors
- store user-added internet radio station URLs
- expose source read/write methods for API handlers

Podcast feeds are unique because a podcast can come from local files through `internal/scanner` or from RSS through `internal/sources`. Both paths write into the same shelf podcast and episode tables so clients can use one podcast API shape.

Internet radio stations are simpler source records: Samo stores the stream URL and descriptive metadata, then exposes public playlist/redirect links. They do not create shelf items and they do not participate in the 24/7 scheduler yet.

`internal/sources` may write SQL because it owns these source ingestion workflows. It should not handle HTTP and should not become a general catalog query service.

### `internal/api`

Owns HTTP routing and response writing.

Current namespaces:

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
- `metadata` searches external metadata providers only when explicitly called.
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

- scanner does not prune rows for files removed from disk
- scanner does not fetch remote metadata; remote RSS ingestion lives in `internal/sources`
- external metadata lookup is search-only; it does not apply changes to catalog items yet
- scanner discovers local cover images but does not extract embedded cover art yet
- scanner does not download or generate covers
- scanner does not transcode audio
- scanner does not yet support manual library management over HTTP
- catalog service refreshes after watcher-triggered scans, but there is not yet a manual scan API
- radio config is still JSON-backed rather than catalog-backed
- internet radio stations are stored and shared, but not probed for live metadata yet
- podcast feed refresh is manual through the API; there is no background feed poller yet
- podcast feed deletion removes the remote podcast shelf item; it does not reconcile with a local-file podcast of the same show yet
- auth is token-gated API access, not full users/sessions yet

## Near-Term Build Path

Useful next steps:

1. Expand scanner metadata extraction to cover the full Samo client needs.
2. Add library management and scan trigger API endpoints.
3. Add deletion/pruning and scan status records.
4. Move playback progress/rating/favorite state into explicit write APIs.
5. Add cover discovery and image serving.
6. Add streaming endpoints for catalog media files.
7. Add scheduled podcast feed polling and feed refresh status.
8. Add metadata candidate apply/merge workflows with user confirmation.
9. Add Subsonic/OpenSubsonic compatibility routes.
10. Add Audiobookshelf compatibility routes.
11. Make radio stations optionally source from catalog queries/playlists.

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

Keep tests focused on contracts and package boundaries. Scanner tests should prefer parser/grouping tests and small generated media fixtures when practical.
