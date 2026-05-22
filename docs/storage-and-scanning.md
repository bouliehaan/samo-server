# Storage and Scanning

Samo uses SQLite as the catalog database. The server applies embedded migrations on startup, then optionally scans configured library folders before serving the API.

## Environment

```sh
SAMO_DB_PATH=/srv/samo/samo.db
SAMO_MUSIC_DIRS=/srv/media/music
SAMO_AUDIOBOOK_DIRS=/srv/media/audiobooks
SAMO_PODCAST_DIRS=/srv/media/podcasts
SAMO_SCAN_ON_START=true
SAMO_WATCH_LIBRARIES=true
SAMO_WATCH_DEBOUNCE=3s
```

Multiple directories use the OS path-list separator. On Linux and macOS that means `:`.

```sh
SAMO_MUSIC_DIRS="/srv/music-a:/srv/music-b"
```

Defaults:

- `SAMO_DB_PATH` defaults to `data/samo.db`
- `SAMO_SCAN_ON_START` defaults to `true`
- `SAMO_WATCH_LIBRARIES` defaults to `true`
- `SAMO_WATCH_DEBOUNCE` defaults to `3s`
- if no library directories are set, startup only migrates and loads the existing database

## File Watching

When `SAMO_WATCH_LIBRARIES` is enabled, Samo recursively watches configured library folders for new writes. Events are debounced so copying a multi-file album or audiobook triggers one scan after the writes settle.

The watcher responds to:

- audio files
- `.opf` sidecars
- `desc.txt`, `description.txt`, `summary.txt`
- `reader.txt`, `narrator.txt`, `narrators.txt`
- local cover images: `jpg`, `jpeg`, `png`, `webp`

After a watch-triggered scan, Samo reloads the catalog from SQLite and updates the in-memory API catalog, so clients can see new files without a server restart.

## Scanner Requirements

The first scanner uses `ffprobe`, so `ffmpeg` should be installed on the server. The scanner walks configured folders and accepts common audio extensions: `mp3`, `flac`, `m4a`, `m4b`, `aac`, `ogg`, `opus`, `wav`, `aif`, `aiff`, `alac`, and `wma`.

## Music Scanner

Music files are scanned as tracks. The scanner extracts:

- title, sort title, subtitle, artist, display artist, album artist, display album artist, album, album sort title, album version, compilation flag
- disc/track numbers, date/year, original release date, release type/status, label, catalog number, barcode, genre, style, mood, tags, comments, lyrics, BPM, key, explicit flag
- MusicBrainz, Discogs, Spotify, Apple Music, and ISRC tags when present
- local cover images from album folders
- audio file container, MIME type, codec, metadata format, bitrate, bit depth, sample rate, channels, duration, size, modified time, inode, and embedded tags

Artists and albums are created from tags with deterministic IDs.

## Audiobook Scanner

Audiobooks are grouped by folder. For common layouts like `/Audiobooks/Author/Book/file.mp3`, Samo treats `Author/Book` as the audiobook item.

The scanner extracts:

- title, subtitle, authors, narrators, series, publisher, date/year, description, language, genres, tags, ISBN/ASIN provider IDs
- explicit and abridged flags
- `desc.txt`, `reader.txt`, and `.opf` sidecar metadata
- chapters from embedded chapter data and OverDrive MediaMarkers
- fallback chapters from file parts when embedded chapters are missing
- local cover images from book folders
- audio file technical metadata for every part

## Podcast Scanner

Podcasts are grouped by first folder under the configured podcast library. Each audio file becomes an episode for that podcast.

The scanner extracts:

- podcast title, author, description, feed URL, site URL, language, owner, categories, feed GUID, iTunes ID
- podcast and episode explicit flags
- episode title, subtitle, description, date, season/episode number, episode type, enclosure URL/type/size, GUID/provider IDs
- episode chapters and audio technical metadata
- local cover images from podcast folders

## Remote Sources

Remote podcast feeds and internet radio stations are handled by `internal/sources`, not by the filesystem scanner.

Podcast RSS feeds:

- `POST /api/v1/shelf/podcast-feeds` fetches and parses a feed URL
- Samo creates a remote "Podcast Feeds" shelf library automatically
- the feed becomes a normal shelf podcast item
- RSS items become podcast episodes with GUID, publish date, duration, season/episode, enclosure URL/type/size, categories, owner, and iTunes metadata when present
- the catalog projection is reloaded after a feed is added, refreshed, or deleted

Internet radio stations:

- `POST /api/v1/internet-radio/stations` stores a station name, stream URL, and optional descriptive metadata
- stations can be listed through the authenticated API
- public M3U and redirect links are available for audio clients
- stations do not create shelf items and are not part of the 24/7 scheduler yet

## External Metadata Lookup

External metadata search is handled by `internal/metadata`, not by the filesystem scanner. It is disabled by default and only runs when an authenticated client explicitly calls `/api/v1/metadata/search`.

Use `SAMO_METADATA_PROVIDERS` to enable providers for future manual enrichment workflows. Search results are candidates only; they are not automatically written back to scanned library items.

## Current Limits

This is the first durable scanner/source layer. It does not yet remove database rows for files deleted from disk, extract embedded cover art, download covers, transcode audio, probe live internet radio metadata, or refresh podcast feeds on a background schedule. It is intentionally built so those pieces can be added behind the same catalog schema and API contracts.
