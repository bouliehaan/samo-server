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

There is no apply/merge endpoint yet. Search results are candidates. A later web UI should let the user compare current catalog metadata against candidate metadata, select fields, and then write those fields through explicit catalog update APIs.

Do not call metadata providers from the scanner, watcher, source ingestion, or startup flow.
