# Metadata Lookup

Samo has an optional external metadata lookup subsystem for user-initiated searches. It is deliberately separate from scanning and catalog hydration.

## Disabled by Default

No provider is enabled unless `SAMO_METADATA_PROVIDERS` is set. With the default empty provider list, metadata search routes perform no outbound network calls and return no candidates.

```sh
SAMO_METADATA_PROVIDERS=openlibrary,googlebooks,itunes,musicbrainz
SAMO_METADATA_USER_AGENT="SamoServer/0.1 (you@example.com)"
```

`SAMO_METADATA_USER_AGENT` is especially important for MusicBrainz, which expects API clients to identify themselves.

## Providers

- `openlibrary`: searches Open Library for audiobook/book metadata candidates.
- `googlebooks`: searches Google Books volumes for audiobook/book metadata candidates.
- `itunes`: searches Apple's iTunes Search API for podcast metadata candidates.
- `musicbrainz`: searches MusicBrainz for music artist, release-group/album, and recording/track candidates.

## Routes

- `GET /api/v1/metadata/providers`
- `GET /api/v1/metadata/search`

Examples:

```text
GET /api/v1/metadata/search?kind=audiobook&title=Signal+Manual&author=Ada+Archive
GET /api/v1/metadata/search?kind=audiobook&isbn=9780000000001&provider=openlibrary
GET /api/v1/metadata/search?kind=podcast&q=Night+Signals&provider=itunes
GET /api/v1/metadata/search?kind=music&musicType=artist&artist=The+Static&provider=musicbrainz
GET /api/v1/metadata/search?kind=music&musicType=album&album=Night+Broadcasts&artist=The+Static&provider=musicbrainz
GET /api/v1/metadata/search?kind=music&musicType=track&track=Signal+One&artist=The+Static&provider=musicbrainz
```

## Apply Workflow

Search results are candidates only until a client explicitly applies them.

1. `GET /api/v1/metadata/search` — find candidates.
2. `POST /api/v1/metadata/apply/preview` — show `before`, merged `after`, plus `appliedFields` / `skippedFields` for the requested field list.
3. `POST /api/v1/metadata/apply` — write selected fields to SQLite and refresh the in-memory catalog.

Apply targets:

- `shelf-item` (audiobook `book` metadata or local `podcast` metadata)
- `shelf-episode`
- `music-artist`, `music-album`, `music-track`
- `podcast-feed` (remote RSS source rows)

Request body:

```json
{
  "targetKind": "shelf-item",
  "targetId": "item_abc123",
  "fields": ["title", "description", "authors", "externalIds"],
  "candidate": {
    "provider": "openlibrary",
    "mediaType": "audiobook",
    "title": "Signal Manual",
    "description": "A dense field guide",
    "authors": [{ "name": "Ada Archive", "role": "author" }]
  }
}
```

`fields` is required and acts as the user confirmation gate. Empty candidate values are reported in `skippedFields` and are not written. `externalIds` merges into existing IDs instead of replacing them.

Do not call metadata providers from the scanner, watcher, source ingestion, or startup flow.
