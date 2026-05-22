# Installing on Ubuntu Server

Samo Server targets Ubuntu Linux. You do not need to install `ffmpeg` or `ffprobe` from apt — the release bundle includes static binaries.

## Release layout

Copy these files together:

```text
/opt/samo/
  samo-server
  bin/ffmpeg
  bin/ffprobe
  data/
```

The server resolves tools from `bin/` next to the `samo-server` executable. Optional overrides:

- `SAMO_FFMPEG_PATH`
- `SAMO_FFPROBE_PATH`

## Build on Ubuntu (or Linux CI)

```sh
make setup    # downloads linux ffmpeg/ffprobe into assets/ and bin/
make build    # produces dist/samo-server + dist/bin/*
```

Cross-compile from another machine for Ubuntu amd64:

```sh
make GOOS=linux GOARCH=amd64 build
```

ARM64 Ubuntu:

```sh
make GOARCH=arm64 bundle-linux
make GOARCH=arm64 build
```

## Configure libraries

```sh
export SAMO_MUSIC_DIRS=/srv/music
export SAMO_AUDIOBOOK_DIRS=/srv/audiobooks
export SAMO_PODCAST_DIRS=/srv/podcasts
export SAMO_DATA_DIR=/opt/samo/data
./samo-server
```

See [storage-and-scanning.md](storage-and-scanning.md) for all scanner environment variables.
