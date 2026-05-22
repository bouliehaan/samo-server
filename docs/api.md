# Samo Native API

Samo's first API is native to this server. Compatibility adapters can sit beside it later, but these routes are the contracts Samo clients should prefer.

All `/api/v1/*` routes accept `Authorization: Bearer <token>` or `X-Samo-Token: <token>` when `SAMO_API_TOKEN` is set.

## Catalog

- `GET /api/v1/catalog/overview`
- `GET /api/v1/catalog/manifest`

`overview` returns counts for music and shelf content. `manifest` returns namespaces, route lists, and the metadata groups clients can expect.

## Libraries

Filesystem libraries are stored in SQLite. Env-configured paths from `SAMO_MUSIC_DIRS`, `SAMO_AUDIOBOOK_DIRS`, and `SAMO_PODCAST_DIRS` are synced into the database on startup.

Routes:

- `GET /api/v1/libraries`
- `GET /api/v1/libraries/{id}`
- `POST /api/v1/libraries`
- `PATCH /api/v1/libraries/{id}`
- `DELETE /api/v1/libraries/{id}`
- `POST /api/v1/libraries/{id}/scan`
- `POST /api/v1/scan`
- `GET /api/v1/scan/jobs`
- `GET /api/v1/scan/jobs/{id}`

`POST /api/v1/libraries` accepts:

```json
{
  "name": "Audiobooks",
  "kind": "shelf",
  "mediaType": "book",
  "path": "/media/audiobooks"
}
```

Supported `kind` values:

- `music`
- `shelf` with `mediaType` of `book` or `podcast`

Scan routes run synchronously and return a scan job record with prune counts. A scan removes database rows for files, shelf items, and local podcast episodes that disappeared from disk since the previous scan.

`PATCH /api/v1/libraries/{id}` may include a new `path`. Relocating a library creates a new deterministic library ID and moves child rows to it.

## Playback

Playback state is stored in SQLite and surfaced on catalog reads after refresh.

Routes:

- `GET /api/v1/playback/{kind}/{id}`
- `PUT /api/v1/playback/{kind}/{id}`
- `PATCH /api/v1/playback/{kind}/{id}`

Supported `kind` values:

- `music-artist`, `music-album`, `music-track`, `music-playlist`
- `shelf-item`, `shelf-episode`

`PATCH` accepts partial fields plus optional `incrementPlayCount`, `incrementSkipCount`, `touchLastPlayedAt`, and `touchLastPositionAt`. Ratings must be 0–5.

Example:

```json
PATCH /api/v1/playback/music-track/track-id
{
  "progressSeconds": 184,
  "favorite": true,
  "touchLastPositionAt": true
}
```

## Media Streaming

Local files are served only when their path falls under a configured filesystem library root.

Routes:

- `GET /api/v1/media/files/{id}`
- `GET /api/v1/media/files/{id}/stream`
- `GET /api/v1/music/tracks/{id}/stream`
- `GET /api/v1/music/albums/{id}/cover`
- `GET /api/v1/shelf/items/{id}/stream`
- `GET /api/v1/shelf/items/{id}/cover`
- `GET /api/v1/shelf/episodes/{id}/stream`

Stream routes support HTTP Range requests. Pass `?mediaFileId=` to stream a specific linked audio file on track, shelf item, and episode shortcuts.

Cover routes serve the first local image path on the album or shelf item (sidecar file or extracted embedded art).

### Extracted covers

When the scanner extracts embedded artwork, covers are cached under `{SAMO_DATA_DIR}/covers` and registered in `extracted_covers`.

Routes:

- `GET /api/v1/media/covers/{id}`
- `GET /api/v1/media/covers/{id}/image`

Catalog `Image` entries use the stable `cover_*` ID and local cache path when extraction ran during scan.

## Metadata Lookup

External metadata lookup is explicit and disabled by default. It is meant for a future web UI where a user asks Samo to search for candidates, reviews the result, then applies selected fields later.

Enable providers with:

```sh
SAMO_METADATA_PROVIDERS=openlibrary,googlebooks,itunes,musicbrainz
SAMO_METADATA_USER_AGENT="SamoServer/0.1 (you@example.com)"
```

Routes:

- `GET /api/v1/metadata/providers`
- `GET /api/v1/metadata/search`

Search examples:

```text
GET /api/v1/metadata/search?kind=audiobook&title=Signal+Manual&author=Ada+Archive
GET /api/v1/metadata/search?kind=audiobook&isbn=9780000000001&provider=openlibrary
GET /api/v1/metadata/search?kind=podcast&q=Night+Signals&provider=itunes
GET /api/v1/metadata/search?kind=music&musicType=track&track=Signal+One&artist=The+Static&provider=musicbrainz
GET /api/v1/metadata/search?kind=music&musicType=album&album=Night+Broadcasts&artist=The+Static&provider=musicbrainz
```

Supported initial providers:

- `openlibrary`: audiobook/book candidates from Open Library
- `googlebooks`: audiobook/book candidates from Google Books
- `itunes`: podcast candidates from Apple's iTunes Search API
- `musicbrainz`: music artist, album/release-group, and track/recording candidates from MusicBrainz

Search returns candidate metadata only. It does not write catalog changes.

## Music

- `GET /api/v1/music/artists`
- `GET /api/v1/music/artists/{id}`
- `GET /api/v1/music/albums`
- `GET /api/v1/music/albums/{id}`
- `GET /api/v1/music/tracks`
- `GET /api/v1/music/tracks/{id}`
- `GET /api/v1/music/genres`
- `GET /api/v1/music/playlists`
- `GET /api/v1/music/playlists/{id}`
- `GET /api/v1/music/search?q=`

List routes support `limit` and `offset`.

Music metadata is intentionally richer than a simple file browser:

- artist sort names, disambiguation, biography, country, genres, styles, moods, links, images, external IDs, counts, playback state
- album artists, track artists, release and original release dates, release type/status, label, catalog number, barcode, genres, styles, moods, tags, images, external IDs, playback state
- track artists, album linkage, disc/track totals, release data, lyrics, BPM, key, comments, audio technical metadata, images, external IDs, playback state
- audio file container, MIME type, codec/profile, bitrate, bit depth, sample rate, channels, duration, size, checksum, embedded tags

## Shelf

The shelf namespace is Samo's Audiobookshelf-shaped side: audiobooks, podcasts, authors, series, library items, files, chapters, and listening progress.

- `GET /api/v1/shelf/libraries`
- `GET /api/v1/shelf/libraries/{id}`
- `GET /api/v1/shelf/items`
- `GET /api/v1/shelf/items/{id}`
- `GET /api/v1/shelf/audiobooks`
- `GET /api/v1/shelf/authors`
- `GET /api/v1/shelf/authors/{id}`
- `GET /api/v1/shelf/series`
- `GET /api/v1/shelf/series/{id}`
- `GET /api/v1/shelf/podcasts`
- `GET /api/v1/shelf/podcast-feeds`
- `POST /api/v1/shelf/podcast-feeds`
- `GET /api/v1/shelf/podcast-feeds/{id}`
- `PATCH /api/v1/shelf/podcast-feeds/{id}`
- `POST /api/v1/shelf/podcast-feeds/poll`
- `POST /api/v1/shelf/podcast-feeds/{id}/refresh`
- `DELETE /api/v1/shelf/podcast-feeds/{id}`
- `GET /api/v1/shelf/episodes`
- `GET /api/v1/shelf/episodes/{id}`
- `GET /api/v1/shelf/search?q=`

List routes support `limit` and `offset`.

Shelf metadata includes:

- library item identity, library ID, media type, filesystem path, inode, size, missing/invalid flags, cover, tags, genres, duration, progress, audio files, chapters
- book title, subtitle, sort title, authors, narrators, series sequence, publisher, published date/year, description, language, ISBNs, explicit/abridged flags, external IDs
- author and series summaries with item counts, duration, images, and external IDs
- podcast feed URL, site URL, owner, language, explicit flag, categories, episode count, external IDs
- podcast episode title, subtitle, description, published date, season/episode numbers, enclosure metadata, chapters, audio files, progress, external IDs

Podcast feeds are remote source records. `POST /api/v1/shelf/podcast-feeds` accepts:

```json
{
  "url": "https://example.com/show/feed.xml",
  "title": "Optional Display Override"
}
```

Samo fetches the RSS feed, stores the feed source, creates or updates a shelf podcast item, and creates or updates remote podcast episodes with enclosure metadata. Local podcast files still come from the scanner and use the same shelf podcast/episode response models.

Feed responses include a `poll` object: `pollEnabled`, `pollIntervalSeconds` (900–604800), `nextPollAt`, `lastPollStartedAt`, `lastPollFinishedAt`, and `consecutiveErrors`.

`PATCH /api/v1/shelf/podcast-feeds/{id}` accepts optional `title`, `pollEnabled`, and `pollIntervalSeconds` without re-fetching RSS.

`POST /api/v1/shelf/podcast-feeds/poll` runs one poll cycle for all due feeds and returns `{ checked, updated, failed, skipped, results[] }`.

When `SAMO_PODCAST_POLL=true` (default), the server also polls due feeds on a background ticker (`SAMO_PODCAST_POLL_TICK`, default `1m`).

## Radio

- `GET /api/v1/radio/stations`
- `GET /api/v1/radio/stations/{id}`
- `GET /api/v1/radio/stations/{id}/now`
- `GET /api/v1/radio/stations/{id}/schedule?from=2026-01-01T00:00:00Z&limit=24`
- `GET /radio/{id}/playlist.m3u`
- `GET /radio/{id}/stream`

The playlist and stream routes stay public so audio clients can open them directly.

## Internet Radio

Internet radio stations are user-added external streams. They are separate from Samo's programmed 24/7 radio stations.

- `GET /api/v1/internet-radio/stations`
- `POST /api/v1/internet-radio/stations`
- `GET /api/v1/internet-radio/stations/{id}`
- `DELETE /api/v1/internet-radio/stations/{id}`
- `GET /internet-radio/{id}/playlist.m3u`
- `GET /internet-radio/{id}/stream`

`POST /api/v1/internet-radio/stations` accepts:

```json
{
  "name": "Static FM",
  "streamUrl": "https://radio.example.com/live.mp3",
  "homepageUrl": "https://radio.example.com",
  "contentType": "audio/mpeg",
  "codec": "mp3",
  "bitrate": 128000,
  "tags": ["old time radio", "drama"]
}
```

The public playlist route writes an M3U pointing at the original stream URL. The public stream route redirects to the original stream URL.

## Compatibility Direction

Navidrome compatibility mostly means OpenSubsonic/Subsonic behavior for music clients. Audiobookshelf compatibility mostly means bearer-token API access to library items with rich book and podcast media metadata. Samo's native API is deliberately shaped so those compatibility layers can map into it without flattening metadata.
