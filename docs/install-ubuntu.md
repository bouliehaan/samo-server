# Installing on Ubuntu

Samo Server ships as a single static binary plus bundled ffmpeg/ffprobe.
There is no apt dependency, no runtime, and no CGO toolchain on the target
host. If you have a stock Ubuntu (or Debian-class) box with systemd, you
have everything you need.

## Quick install

```sh
tar xzf samo-server-linux-amd64.tar.gz
cd samo-server-linux-amd64
sudo ./install.sh
```

That's the whole install. The script:

- creates a `samo` system user
- installs to `/opt/samo/` (binary + `bin/ffmpeg` + `bin/ffprobe`)
- creates the data directory at `/var/lib/samo`
- drops a hardened systemd unit at `/etc/systemd/system/samo-server.service`
- enables and starts the service

When it finishes, it prints the URL to open:

```
Setup wizard: http://<your-ip>:6969/setup
```

Open that in a browser to create the first admin account and attach
your media folders. No env vars to set, no terminal commands to copy.

## Upgrading

Re-running `install.sh` from a newer release tarball upgrades in place.
The script detects an existing install and switches to upgrade mode:

```sh
tar xzf samo-server-linux-amd64.tar.gz
cd samo-server-linux-amd64
sudo ./install.sh
==> upgrading existing samo-server install (data at /var/lib/samo preserved)
==> installing into /opt/samo
==> installing systemd unit
==> starting samo-server
Samo Server upgraded and restarted.
```

What gets touched:

- `/opt/samo/samo-server` and `/opt/samo/bin/{ffmpeg,ffprobe}` are
  replaced atomically.
- `/etc/systemd/system/samo-server.service` is replaced. Any drop-in
  overrides under `samo-server.service.d/` are preserved (systemd merges
  them).
- The service is restarted; new schema migrations apply on startup
  (additive only — no destructive changes).

What stays untouched:

- `/var/lib/samo/*` — the entire data directory, including SQLite,
  cover cache, podcast cache, and metadata overrides.
- The `samo` system user.
- Your library mounts.

If a migration ever needs special handling, the release notes will
call it out. As a rule, normal upgrades are: extract → run install.sh.

For belt-and-braces, snapshot the data dir before upgrade:

```sh
sudo tar czf samo-backup-$(date +%F).tar.gz /var/lib/samo
```

## Day 2 commands

```sh
sudo systemctl status samo-server   # health check
sudo systemctl restart samo-server  # bounce
sudo journalctl -u samo-server -f   # follow logs
sudo /opt/samo/uninstall.sh         # remove (keeps /var/lib/samo)
sudo /opt/samo/uninstall.sh --purge # remove + wipe data
```

## Customizing the service

The unit ships with sensible defaults. To override anything (port,
data dir, env vars, hardening flags) without editing the shipped unit:

```sh
sudo systemctl edit samo-server
```

systemd opens an empty drop-in file at
`/etc/systemd/system/samo-server.service.d/override.conf`. Add only the
fields you want to change, e.g.:

```ini
[Service]
Environment=SAMO_ADDR=:8080
Environment=SAMO_LASTFM_API_KEY=...
Environment=SAMO_LASTFM_SHARED_SECRET=...
```

Save, then `sudo systemctl restart samo-server`.

## Explo folder auto-tagging (optional)

If you run a weekly playlist-exporter integration ("explo" or similar) that
drops untagged music files into a folder, Samo can identify them in the
background (via audio fingerprinting, never by trusting the filename),
route them into a system-managed "Explo" playlist, and keep them out of the
Recently Added shelves — without moving, renaming, or writing tags to the
files themselves. The exporter keeps managing the folder exactly as before.

This is opt-in and needs two things beyond the base install:

1. A free [AcoustID](https://acoustid.org/new-application) API key.
2. The `fpcalc` binary next to `ffmpeg`/`ffprobe`. If you built the release
   tarball yourself, run `make bundle-chromaprint` (or `bundle-chromaprint-all`
   for both architectures) before `make release-amd64` — `bin/fpcalc` then
   ships in the tarball automatically. Official release tarballs already
   include it.

Then set:

```ini
[Service]
Environment=SAMO_EXPLO_DIRS=/srv/music/explo
Environment=SAMO_ACOUSTID_API_KEY=...
```

`SAMO_EXPLO_DIRS` accepts multiple paths separated by `:` (same convention
as `SAMO_MUSIC_DIRS`). Leave it unset and the feature stays fully off.

## Building a release locally

If you want to produce the release tarball yourself instead of
downloading one:

```sh
make release-amd64    # linux/amd64 tarball into dist/
make release-arm64    # linux/arm64 tarball into dist/
```

Each tarball contains everything the install script needs.

## File layout after install

```
/opt/samo/
  samo-server         pure-Go binary, no CGO
  bin/ffmpeg          static GPL ffmpeg
  bin/ffprobe
/var/lib/samo/
  samo.db             SQLite catalog
  covers/             cover art cache
  podcast-cache/      enclosure cache
/etc/systemd/system/
  samo-server.service
```

## Custom media paths

The default hardened unit mounts `/srv`, `/mnt`, `/media`, and `/var/lib/samo`
read-write for the service user. If your music lives under `/data` or
similar, either move the library or relax `ProtectSystem` via a drop-in:

```ini
[Service]
ProtectSystem=false
```

Then `sudo systemctl daemon-reload && sudo systemctl restart samo-server`.

## Manual install (without the script)

You can also drop the binary anywhere and run it directly:

```sh
SAMO_DATA_DIR=/srv/samo SAMO_ADDR=:6969 /opt/samo/samo-server
```

The first request to `/` redirects to `/setup` until you've created an
admin and attached a library — the same wizard flow the install script
bootstraps you into.
