#!/usr/bin/env bash
set -euo pipefail

# Samo Server targets Ubuntu Linux. This script downloads the official static
# chromaprint fpcalc build (used by the explo folder feature to fingerprint
# untagged audio) and lays it out the same way bundle-ffmpeg.sh does, so the
# existing internal/toolchain bundled-asset resolution picks it up for free.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSETS_DIR="${ROOT}/internal/toolchain/assets"
BIN_DIR="${ROOT}/bin"
VERSION="1.6.0"

usage() {
  cat <<EOF
Usage: ./scripts/bundle-chromaprint.sh [--all] [--platform linux-amd64|linux-arm64]

Downloads the static fpcalc binary (AcoustID/chromaprint releases) for
Ubuntu/Linux.

Default platform on Linux:
  linux-amd64 on x86_64 (typical Ubuntu server)
  linux-arm64 on aarch64 (ARM Ubuntu)

--all bundles both linux-amd64 and linux-arm64 into assets/.

Release layout (copy to your server as-is, alongside ffmpeg/ffprobe):
  samo-server
  bin/fpcalc
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

chromaprint_archive() {
  case "$1" in
    linux-amd64) printf '%s\n' "chromaprint-fpcalc-${VERSION}-linux-x86_64.tar.gz" ;;
    linux-arm64) printf '%s\n' "chromaprint-fpcalc-${VERSION}-linux-arm64.tar.gz" ;;
    *) echo "unsupported platform: $1" >&2; exit 1 ;;
  esac
}

cache_path() {
  printf '%s\n' "${ROOT}/.cache/chromaprint/$1"
}

download_archive() {
  local key="$1"
  local archive dest
  archive="$(chromaprint_archive "${key}")"
  dest="$(cache_path "${archive}")"
  mkdir -p "$(dirname "${dest}")"
  if [[ ! -f "${dest}" ]]; then
    echo "==> Downloading ${archive}" >&2
    curl -fL --retry 3 --retry-delay 2 \
      -o "${dest}" \
      "https://github.com/acoustid/chromaprint/releases/download/v${VERSION}/${archive}"
  fi
  printf '%s\n' "${dest}"
}

install_platform() {
  local key="$1"
  local archive dest_dir tmp_dir fpcalc_src
  archive="$(download_archive "${key}")"
  dest_dir="${ASSETS_DIR}/${key}"
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/samo-chromaprint.XXXXXX")"
  mkdir -p "${dest_dir}"

  echo "==> Extracting ${key}" >&2
  tar -xzf "${archive}" -C "${tmp_dir}"

  fpcalc_src="$(find "${tmp_dir}" -type f -name fpcalc | head -n 1)"
  if [[ -z "${fpcalc_src}" ]]; then
    echo "fpcalc not found in ${archive}" >&2
    exit 1
  fi

  install -m 0755 "${fpcalc_src}" "${dest_dir}/fpcalc"
  rm -rf "${tmp_dir}"
  echo "==> Installed ${key} to ${dest_dir}" >&2
}

install_bin_for_platform() {
  local key="$1"
  mkdir -p "${BIN_DIR}"
  install -m 0755 "${ASSETS_DIR}/${key}/fpcalc" "${BIN_DIR}/fpcalc"
  echo "==> Installed ${BIN_DIR}/fpcalc (${key})" >&2
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

  install_platform "${platform}"
  install_bin_for_platform "${platform}"
}

main "$@"
