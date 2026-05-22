# Bundled ffmpeg assets (Ubuntu Linux)

Samo Server ships static `ffmpeg` and `ffprobe` for **Ubuntu/Linux** only.

Populate this directory on a Linux build host (or CI) with:

```sh
./scripts/bundle-ffmpeg.sh --all
```

Typical Ubuntu server (x86_64):

```sh
./scripts/bundle-ffmpeg.sh
# or explicitly:
./scripts/bundle-ffmpeg.sh --platform linux-amd64
```

ARM Ubuntu:

```sh
./scripts/bundle-ffmpeg.sh --platform linux-arm64
```

Release builds embed these paths when compiled with `-tags bundled`. The normal release tarball places binaries in `bin/` next to `samo-server` instead.
