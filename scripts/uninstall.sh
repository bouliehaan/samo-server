#!/usr/bin/env bash
# Remove samo-server from a systemd host. Leaves the data directory by
# default so a re-install can pick up where the previous one left off.
# Pass --purge to remove /var/lib/samo too.

set -euo pipefail

if [ "${EUID}" -ne 0 ]; then
  echo "samo-server uninstaller must run as root (try: sudo $0)" >&2
  exit 1
fi

INSTALL_DIR="${SAMO_INSTALL_DIR:-/opt/samo}"
DATA_DIR="${SAMO_DATA_DIR:-/var/lib/samo}"
USER_NAME="${SAMO_USER:-samo}"
GROUP_NAME="${SAMO_GROUP:-samo}"
UNIT_PATH="/etc/systemd/system/samo-server.service"

PURGE=0
for arg in "$@"; do
  case "${arg}" in
    --purge) PURGE=1 ;;
    *) echo "unknown flag: ${arg}" >&2; exit 1 ;;
  esac
done

if systemctl list-unit-files | grep -q '^samo-server.service'; then
  echo "==> stopping samo-server"
  systemctl stop samo-server.service || true
  systemctl disable samo-server.service || true
fi

if [ -f "${UNIT_PATH}" ]; then
  echo "==> removing systemd unit"
  rm -f "${UNIT_PATH}"
  systemctl daemon-reload
fi

if [ -d "${INSTALL_DIR}" ]; then
  echo "==> removing ${INSTALL_DIR}"
  rm -rf "${INSTALL_DIR}"
fi

if [ "${PURGE}" -eq 1 ]; then
  if [ -d "${DATA_DIR}" ]; then
    echo "==> --purge: removing ${DATA_DIR}"
    rm -rf "${DATA_DIR}"
  fi
  if id -u "${USER_NAME}" >/dev/null 2>&1; then
    echo "==> --purge: removing user ${USER_NAME}"
    userdel "${USER_NAME}" || true
  fi
  if getent group "${GROUP_NAME}" >/dev/null; then
    groupdel "${GROUP_NAME}" || true
  fi
else
  echo "==> keeping data dir at ${DATA_DIR} (pass --purge to remove)"
fi

echo "samo-server uninstalled."
