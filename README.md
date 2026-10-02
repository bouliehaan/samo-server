# samo-server

A self-hosted media server for music, audiobooks, podcasts and radio — one
library, one queue, one history. Not a wrapper around Navidrome or
Audiobookshelf: the four media kinds are first-class domains that share
playback state, recents and browsing.

The clients live in [bouliehaan/samo](https://github.com/bouliehaan/samo) —
Android and desktop.

## Install

On the machine your music is on — Ubuntu, Debian or Raspberry Pi OS, 64-bit.
Two lines, and you only need the first one once.

**Docker**, if `docker --version` says it is not installed:

```bash
curl -fsSL https://get.docker.com | sudo sh
```

That is Docker's own installer. Use it rather than the `snap install docker`
Ubuntu suggests: the snap is sandboxed away from most of your folders.

**samo**, told where your music is:

```bash
sudo SAMO_MEDIA_DIR=/home/you/Music docker compose -f oci://ghcr.io/bouliehaan/samo-server:compose up -d -y
```

Then open `http://<this machine>:6969/setup` from any computer on your network
and the wizard takes it from there — admin account, library folders, first
scan. Your music folder is the first one it suggests. If you do not know this
machine's address, `docker logs samo-server` prints the setup link, and
`hostname -I` prints the address on its own.

`SAMO_MEDIA_DIR` can be any folder: your home's Music folder, an external drive
under `/media`, a disk under `/mnt`. Left out, it is `/mnt/media`. samo runs as
the machine's first user account (uid 1000) so it can write into that folder —
**Keep in Library** copies into it; if your files belong to a different
account, add `PUID=$(id -u) PGID=$(id -g)` before `docker compose`.

To keep the rest of the drive read-only, so a scanner bug cannot eat a library,
also name the one folder samo may write to, inside it:

```bash
sudo SAMO_MEDIA_DIR=/srv/media SAMO_MUSIC_DIR=/srv/media/music \
  docker compose -f oci://ghcr.io/bouliehaan/samo-server:compose up -d -y
```

**Updating** is the line you installed with, with `--pull always` on the end.
Keep the same settings: an update without `SAMO_MEDIA_DIR` brings samo up
looking at `/mnt/media`, and your library shows as missing — nothing is
deleted — until you run it again with them. The compose artifact lives in the
same registry as the image and pins it by digest, so a given tag always brings
up exactly the build it was published with.

Installed before 1.4.0? The stack was named after whatever folder the command
ran in, and so were its volumes. `sudo docker compose ls` shows that name; put
`-p <name>` after `docker compose` on your update line and samo keeps the data
it already has.

Radio schedules follow this machine's clock. If `timedatectl` shows UTC and
you are not, `sudo timedatectl set-timezone America/Denver` (your zone), or set
`SAMO_TIMEZONE` on the install line.

### Ports, and the one that gets forgotten

`6969/tcp` is the web UI and API. `7360/udp` is LAN autodiscovery — clients
broadcast `Who is SamoServer?` and get back this machine's real address, which
is how the Android and desktop apps find you without being told an address.

The server uses host networking so both bind to the host directly; Docker's
default bridge drops LAN broadcasts and would advertise an unreachable
container address. That also means your firewall applies to those ports, where
the bridge used to bypass it:

```bash
sudo ufw allow 6969/tcp
sudo ufw allow 7360/udp
```

A blocked `7360/udp` is the usual reason discovery goes silent.

### The database

Postgres runs alongside it with **no password and no TCP listener at all** —
`listen_addresses` is empty and the two containers share a unix socket through a
volume. There is nothing on the network to authenticate to, so there is no
credential to set, leak, or bake into a published compose file.

Optional integrations go on the install line the same way as the media path: `SAMO_LASTFM_API_KEY`, `SAMO_LASTFM_SHARED_SECRET`,
`SAMO_ACOUSTID_API_KEY`, `SAMO_EGRESS_PROXY_URL`.

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

# run a working tree that is ahead of the release
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
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
