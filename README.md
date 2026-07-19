# Samo Server

A unified self-hosted listening server for music, audiobooks, podcasts, and radio.

Samo Server is not a Navidrome wrapper.
Samo Server is not an Audiobookshelf wrapper.
Samo Server is a native media server built around unified listening history, playback state, devices, queues, and cross-media browsing.

## Initial scope

V0 focuses on:

- running as a small Ubuntu-friendly Go server
- PostgreSQL-backed metadata storage
- running a deterministic 24/7 radio station from local media
- adding local library folders
- adding podcast RSS feeds
- adding internet radio stream URLs
- optional user-initiated metadata lookup providers
- scanning music files
- exposing a Samo-native API
- streaming original audio files
- accepting playback/scrobble events
- powering Samo-native recents

## First module: Samo Radio

The first server module is a 24/7 station that rotates configured podcasts, old radio, commercials, music, or other local audio into a streamable endpoint.

- Configure stations with `SAMO_RADIO_CONFIG` or the default `data/radio.json`.
- Open `/radio/{id}/playlist.m3u` in an audio client.
- Use `/api/v1/radio/stations/{id}/now` and `/api/v1/radio/stations/{id}/schedule` for Samo-native clients.

See [docs/radio.md](docs/radio.md) for the config format and current stream behavior.

## API

Samo exposes a native `/api/v1` surface for music, audiobooks, podcasts, radio, and catalog overview data. See [docs/api.md](docs/api.md) for the first route map and metadata contracts.

Music, audiobooks, podcasts, and radio are independent first-class domains. Each has its own URL namespace (`/api/v1/music`, `/api/v1/audiobooks`, `/api/v1/podcasts`, `/api/v1/radio`) and its own DTOs — there is no shared "shelf" / longform parent. Podcast RSS feeds are added through `/api/v1/podcasts/feeds`. Internet radio streams are added through `/api/v1/internet-radio/stations` and get public M3U/redirect links for audio clients.

External metadata lookup is disabled by default and can be enabled later with `SAMO_METADATA_PROVIDERS`. See [docs/metadata.md](docs/metadata.md) for provider names and search routes.

Last.fm scrobbling is configured with `SAMO_LASTFM_API_KEY` and `SAMO_LASTFM_SHARED_SECRET`. See [docs/lastfm.md](docs/lastfm.md) for account linking and playback/scrobble behavior.

## Storage and scanning

Samo stores catalog metadata in **PostgreSQL**, pointed at with `SAMO_DB_DSN=postgres://...` (the bundled `docker compose` sets this up for you). It scans configured music, audiobook, and podcast folders using bundled `ffmpeg`/`ffprobe` on Ubuntu. See [docs/install-ubuntu.md](docs/install-ubuntu.md) for deployment layout and [docs/storage-and-scanning.md](docs/storage-and-scanning.md) for scanner environment variables.

Schema changes are plain SQL files in `migrations/postgres/`, applied automatically at startup; add a new numbered file, never edit an old one.

## Running with Docker + Postgres

The whole stack — server plus Postgres — runs in two containers on a Linux host. `./install.sh` opens the firewall ports and boots everything:

```bash
sudo ./install.sh
# open http://<this machine's LAN IP>:6969
```

The server runs with Docker **host networking**, so it listens on the host directly — HTTP on `6969/tcp` and UDP autodiscovery on `7360/udp`. That is what makes discovery work: LAN broadcasts (`"Who is SamoServer?"`) reach the listener and it replies with the host's real LAN IP. Docker's default bridge can't do this — it drops LAN broadcasts and would advertise an unreachable container-internal address. Postgres stays in its own container, published only on `127.0.0.1`, never exposed to the LAN.

Because host networking means your firewall now applies to those ports (bridge used to bypass it), `install.sh` runs `ufw`/`firewalld allow` for `6969/tcp` and `7360/udp`. A blocked `7360/udp` is the most common reason discovery goes silent. Prefer to do it by hand? Copy `.env.example` to `.env`, allow those two ports, and `docker compose up -d --build`.

Still on an old SQLite install? Samo is Postgres-only now — migrate first using a pre-removal build's `migrate-postgres` command (it copies every row and verifies counts), then upgrade. See **[MIGRATING-TO-POSTGRES.md](MIGRATING-TO-POSTGRES.md)**.

### Running the tests

Tests run against a real PostgreSQL (each test gets its own database cloned from a migrated template). `make test` starts a disposable container on port 55432 and runs the suite; point `SAMO_TEST_PG_DSN` at your own server to skip the container.

## Philosophy

- no jank
- no fake data
- no throwaway glue-server architecture
- small boring reliable pieces
- client talks to Samo-native concepts, not backend-specific hacks
