#!/usr/bin/env bash
# Samo Server installer for Ubuntu/Debian-class systemd hosts.
# Run from inside an extracted release tarball:
#   sudo ./install.sh

set -euo pipefail

if [ "${EUID}" -ne 0 ]; then
  echo "samo-server installer must run as root (try: sudo $0)" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
INSTALL_DIR="${SAMO_INSTALL_DIR:-/opt/samo}"
DATA_DIR="${SAMO_DATA_DIR:-/var/lib/samo}"
USER_NAME="${SAMO_USER:-samo}"
GROUP_NAME="${SAMO_GROUP:-samo}"
UNIT_PATH="/etc/systemd/system/samo-server.service"

# Detect upgrade vs. fresh install so the messaging matches what's
# actually happening. Re-running this script over a previous install is
# fully supported — the binary and unit are replaced atomically and the
# data directory is left alone. Schema migrations run on startup.
MODE="install"
if [ -x "${INSTALL_DIR}/samo-server" ] || [ -f "${UNIT_PATH}" ]; then
  MODE="upgrade"
fi

# ---- locate release artifacts ------------------------------------------------

ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) echo "unsupported architecture: ${ARCH}" >&2; exit 1 ;;
esac

# Prefer arch-suffixed binary name (release tarball), fall back to plain
# samo-server (dev/make build).
BINARY=""
for candidate in "${SCRIPT_DIR}/samo-server-linux-${GOARCH}" "${SCRIPT_DIR}/samo-server"; do
  if [ -f "${candidate}" ]; then BINARY="${candidate}"; break; fi
done
if [ -z "${BINARY}" ]; then
  echo "missing samo-server binary next to install.sh" >&2
  exit 1
fi

FFMPEG="${SCRIPT_DIR}/bin/ffmpeg"
FFPROBE="${SCRIPT_DIR}/bin/ffprobe"
if [ ! -f "${FFMPEG}" ] || [ ! -f "${FFPROBE}" ]; then
  echo "missing bin/ffmpeg or bin/ffprobe next to install.sh" >&2
  exit 1
fi

UNIT_SOURCE="${SCRIPT_DIR}/samo-server.service"
if [ ! -f "${UNIT_SOURCE}" ]; then
  echo "missing samo-server.service next to install.sh" >&2
  exit 1
fi

# ---- create samo system user -------------------------------------------------

if [ "${MODE}" = "upgrade" ]; then
  echo "==> upgrading existing samo-server install (data at ${DATA_DIR} preserved)"
else
  echo "==> installing samo-server (fresh)"
fi

if ! getent group "${GROUP_NAME}" >/dev/null; then
  echo "==> creating group ${GROUP_NAME}"
  groupadd --system "${GROUP_NAME}"
fi
if ! id -u "${USER_NAME}" >/dev/null 2>&1; then
  echo "==> creating user ${USER_NAME}"
  useradd \
    --system \
    --gid "${GROUP_NAME}" \
    --shell /usr/sbin/nologin \
    --home-dir "${DATA_DIR}" \
    --comment "Samo Server" \
    "${USER_NAME}"
fi

# ---- install binaries --------------------------------------------------------

echo "==> installing into ${INSTALL_DIR}"
install -d -m 0755 "${INSTALL_DIR}"
install -d -m 0755 "${INSTALL_DIR}/bin"
install -m 0755 "${BINARY}" "${INSTALL_DIR}/samo-server"
install -m 0755 "${FFMPEG}" "${INSTALL_DIR}/bin/ffmpeg"
install -m 0755 "${FFPROBE}" "${INSTALL_DIR}/bin/ffprobe"

# ---- data directory ----------------------------------------------------------

echo "==> preparing data directory ${DATA_DIR}"
install -d -o "${USER_NAME}" -g "${GROUP_NAME}" -m 0755 "${DATA_DIR}"

# ---- systemd unit ------------------------------------------------------------

echo "==> installing systemd unit"
install -m 0644 "${UNIT_SOURCE}" "${UNIT_PATH}"
systemctl daemon-reload

echo "==> enabling samo-server"
systemctl enable samo-server.service

echo "==> starting samo-server"
systemctl restart samo-server.service

sleep 1
if ! systemctl is-active --quiet samo-server.service; then
  echo
  echo "samo-server failed to start. Recent logs:" >&2
  journalctl -u samo-server.service -n 40 --no-pager >&2 || true
  exit 1
fi

# ---- friendly footer ---------------------------------------------------------

# Best-effort host discovery for the "what URL do I visit" line.
HOST_GUESS=""
if command -v hostname >/dev/null 2>&1; then
  HOST_GUESS="$(hostname -I 2>/dev/null | awk '{print $1}')"
fi
if [ -z "${HOST_GUESS}" ]; then HOST_GUESS="<this-machine-ip>"; fi

if [ "${MODE}" = "upgrade" ]; then
  cat <<EOF

Samo Server upgraded and restarted.

  Open:         http://${HOST_GUESS}:6969/
  Service:      sudo systemctl status samo-server
  Logs:         sudo journalctl -u samo-server -f
  Data:         ${DATA_DIR}  (untouched; schema migrations applied on startup)

EOF
else
  cat <<EOF

Samo Server installed and running.

  Setup wizard: http://${HOST_GUESS}:6969/setup
  Service:      sudo systemctl status samo-server
  Logs:         sudo journalctl -u samo-server -f
  Config:       /etc/systemd/system/samo-server.service.d/  (drop-ins)
  Data:         ${DATA_DIR}
  Uninstall:    sudo ${SCRIPT_DIR}/uninstall.sh

EOF
fi
