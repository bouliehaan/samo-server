#!/usr/bin/env bash
set -euo pipefail

# Samo Server targets Ubuntu Linux. This script downloads static GPL ffmpeg/ffprobe
# builds and lays them out for release packaging and optional go:embed builds.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSETS_DIR="${ROOT}/internal/toolchain/assets"
BIN_DIR="${ROOT}/bin"
VERSION="linux-gpl-latest"

usage() {
  cat <<EOF
Usage: ./scripts/bundle-ffmpeg.sh [--all] [--platform linux-amd64|linux-arm64]

Downloads static ffmpeg and ffprobe for Ubuntu/Linux (BtbN FFmpeg-Builds).

Default platform on Linux:
  linux-amd64 on x86_64 (typical Ubuntu server)
  linux-arm64 on aarch64 (ARM Ubuntu)

--all bundles both linux-amd64 and linux-arm64 into assets/.

Release layout (copy to your server as-is):
  samo-server
  bin/ffmpeg
  bin/ffprobe
EOF
}

normalize_arch() {
  case "$1" in
    x86_64|amd64) printf '%s\n' amd64 ;;
    aarch64|arm64) printf '%s\n' arm64 ;;
    *) echo "unsupported architecture: $1" >&2; exit 1 ;;
  esac
}

linux_platform_key() {
  printf 'linux-%s\n' "$(normalize_arch "$1")"
}

detect_linux_platform() {
  if [[ "$(uname -s)" != "Linux" ]]; then
    echo "run on Ubuntu/Linux, or pass --platform linux-amd64" >&2
    exit 1
  fi
  linux_platform_key "$(uname -m)"
}

btbn_archive() {
  case "$1" in
    linux-amd64) printf '%s\n' ffmpeg-master-latest-linux64-gpl.tar.xz ;;
    linux-arm64) printf '%s\n' ffmpeg-master-latest-linuxarm64-gpl.tar.xz ;;
    *) echo "unsupported platform: $1" >&2; exit 1 ;;
  esac
}

cache_path() {
  printf '%s\n' "${ROOT}/.cache/ffmpeg/$1"
}

download_archive() {
  local key="$1"
  local archive dest
  archive="$(btbn_archive "${key}")"
  dest="$(cache_path "${archive}")"
  mkdir -p "$(dirname "${dest}")"
  if [[ ! -f "${dest}" ]]; then
    echo "==> Downloading ${archive}" >&2
    curl -fL --retry 3 --retry-delay 2 \
      -o "${dest}" \
      "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/${archive}"
  fi
  printf '%s\n' "${dest}"
}

install_platform() {
  local key="$1"
  local archive dest_dir tmp_dir ffmpeg_src ffprobe_src
  archive="$(download_archive "${key}")"
  dest_dir="${ASSETS_DIR}/${key}"
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/samo-ffmpeg.XXXXXX")"
  mkdir -p "${dest_dir}"

  echo "==> Extracting ${key}" >&2
  tar -xJf "${archive}" -C "${tmp_dir}"

  ffmpeg_src="$(find "${tmp_dir}" -type f -name ffmpeg | head -n 1)"
  ffprobe_src="$(find "${tmp_dir}" -type f -name ffprobe | head -n 1)"
  if [[ -z "${ffmpeg_src}" || -z "${ffprobe_src}" ]]; then
    echo "ffmpeg or ffprobe not found in ${archive}" >&2
    exit 1
  fi

  install -m 0755 "${ffmpeg_src}" "${dest_dir}/ffmpeg"
  install -m 0755 "${ffprobe_src}" "${dest_dir}/ffprobe"
  rm -rf "${tmp_dir}"
  echo "==> Installed ${key} to ${dest_dir}" >&2
}

install_bin_for_platform() {
  local key="$1"
  mkdir -p "${BIN_DIR}"
  install -m 0755 "${ASSETS_DIR}/${key}/ffmpeg" "${BIN_DIR}/ffmpeg"
  install -m 0755 "${ASSETS_DIR}/${key}/ffprobe" "${BIN_DIR}/ffprobe"
  echo "==> Installed ${BIN_DIR}/ffmpeg and ${BIN_DIR}/ffprobe (${key})" >&2
}

main() {
  local all=0
  local platform=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --all)
        all=1
        shift
        ;;
      --platform)
        platform="${2:-}"
        if [[ -z "${platform}" ]]; then
          echo "--platform requires a value" >&2
          exit 1
        fi
        shift 2
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        usage
        exit 1
        ;;
    esac
  done

  mkdir -p "${ASSETS_DIR}" "${BIN_DIR}"

  if [[ "${all}" -eq 1 ]]; then
    install_platform "linux-amd64"
    install_platform "linux-arm64"
    if [[ "$(uname -s)" == "Linux" ]]; then
      install_bin_for_platform "$(detect_linux_platform)"
    else
      install_bin_for_platform "linux-amd64"
    fi
    exit 0
  fi

  if [[ -z "${platform}" ]]; then
    if [[ "$(uname -s)" == "Linux" ]]; then
      platform="$(detect_linux_platform)"
    else
      echo "==> Not on Linux; bundling linux-amd64 for Ubuntu server releases" >&2
      platform="linux-amd64"
    fi
  fi

  case "${platform}" in
    linux-amd64|linux-arm64) ;;
    *)
      echo "unsupported platform: ${platform} (Ubuntu targets: linux-amd64, linux-arm64)" >&2
      exit 1
      ;;
  esac

  install_platform "${platform}"
  install_bin_for_platform "${platform}"

  echo "==> Bundled ffmpeg ${VERSION} for ${platform}" >&2
  echo "    Deploy dist/samo-server plus dist/bin/ffmpeg and dist/bin/ffprobe on Ubuntu" >&2
}

main "$@"
