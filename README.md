# samo-server

A self-hosted media server for music, audiobooks, podcasts and radio — one
library, one queue, one history. Not a wrapper around Navidrome or
Audiobookshelf: the four media kinds are first-class domains that share
playback state, recents and browsing.

The clients live in [bouliehaan/samo](https://github.com/bouliehaan/samo) —
Android and desktop.

## Install

Linux host with Docker. Two containers: the server and its Postgres.

```bash
git clone https://github.com/bouliehaan/samo-server.git
cd samo-server
cp .env.example .env      # set POSTGRES_PASSWORD and your media path
sudo ./install.sh
```

Then open `http://<this-machine's-LAN-IP>:6969/setup`.

`install.sh` pulls [`ghcr.io/bouliehaan/samo-server:latest`](https://github.com/bouliehaan/samo-server/pkgs/container/samo-server),
opens the firewall for `6969/tcp` and `7360/udp`, and starts the stack. It is
safe to re-run — that is also how you update.

Prefer to drive compose yourself:

```bash
docker compose pull && docker compose up -d
```

### The two things you must set

In `.env`:

| | |
|---|---|
| `POSTGRES_PASSWORD` | Baked into the database on the first boot. Change it before then. |
| `SAMO_MEDIA_HOST_DIR` | Absolute path to your media, mounted into the container at the **same** path. Point `SAMO_MUSIC_DIRS` / `SAMO_AUDIOBOOK_DIRS` / `SAMO_PODCAST_DIRS` at subfolders of it. |

Everything else in `.env.example` is optional and commented.

### Ports

`6969/tcp` is the web UI and API. `7360/udp` is LAN autodiscovery — clients
broadcast `Who is SamoServer?` and get back this machine's real address.

The server runs with Docker **host networking** so both bind to the host
directly; the default bridge drops LAN broadcasts and would advertise an
unreachable container address. That also means your firewall now applies to
those ports, and a blocked `7360/udp` is the usual reason discovery goes
silent. `install.sh` opens both. Postgres stays in its own container on
`127.0.0.1`, never on the LAN.

## What it does

- Scans local music, audiobook and podcast folders with bundled `ffmpeg`.
- Adds podcast RSS feeds and internet radio stream URLs.
- Runs 24/7 radio channels programmed from your own library — see
  [docs/channels.md](docs/channels.md).
- Streams original files, and serves artwork through a thumbnail ladder.
- Records playback and scrobbles, optionally to Last.fm.
- Exposes a native `/api/v1` surface plus a read-only Subsonic adapter.

## Docs

| | |
|---|---|
| [docs/api.md](docs/api.md) | The `/api/v1` route map and DTOs |
| [docs/channels.md](docs/channels.md) | Radio channels: plans, pools, blocks |
| [docs/radio.md](docs/radio.md) | Station config and stream behaviour |
| [docs/storage-and-scanning.md](docs/storage-and-scanning.md) | Scanner environment variables |
| [docs/metadata.md](docs/metadata.md) | External lookup providers (off by default) |
| [docs/lastfm.md](docs/lastfm.md) | Scrobbling and account linking |
| [docs/samo-radio.md](docs/samo-radio.md) | Aux-port playback devices |

## Developing

```bash
make test     # starts a disposable Postgres on 55432 and runs the suite
```

Tests run against a real PostgreSQL — each gets its own database cloned from a
migrated template. Point `SAMO_TEST_PG_DSN` at your own server to skip the
container.

Schema changes are plain SQL in `migrations/postgres/`, applied at startup. Add
a new numbered file; never edit an old one.

## House rules

- no jank
- no fake data
- no throwaway glue-server architecture
- small boring reliable pieces
- the client talks to samo-native concepts, not backend-specific hacks

## Licence

[GPL-3.0-only](LICENSE), matching the samo client.
