#!/usr/bin/env bash
# Samo Server — Docker installer for Linux hosts.
#
# Boots the server + Postgres stack and opens the firewall ports that LAN
# autodiscovery needs. The server runs with host networking (see
# docker-compose.yml), so it listens on the host directly:
#
#   6969/tcp   web UI + API
#   7360/udp   autodiscovery ("Who is SamoServer?")
#
# With host networking your firewall (ufw) actually applies to those ports —
# unlike Docker's bridge, which quietly bypasses it. This is the single most
# common reason discovery "just doesn't work": the packets are firewalled.
#
#   sudo ./install.sh              open ports + boot
#   sudo ./install.sh --no-up      just open the firewall
#
# Run it from the repo root on the Ubuntu host that will run Samo.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/.env"
ENV_EXAMPLE="${SCRIPT_DIR}/.env.example"

DO_UP=1
for arg in "$@"; do
  case "${arg}" in
    --no-up)   DO_UP=0 ;;
    -h|--help) grep -E '^# ' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: ${arg}" >&2; exit 2 ;;
  esac
done

die()  { echo "error: $*" >&2; exit 1; }
note() { echo "==> $*"; }
warn() { echo "warning: $*" >&2; }

# ---- preflight ---------------------------------------------------------------

[ "$(uname -s)" = "Linux" ] || die "the Docker stack uses host networking, which only behaves correctly on Linux. Deploy to your Ubuntu host."

command -v docker >/dev/null 2>&1 || die "docker is not installed. See https://docs.docker.com/engine/install/"
if docker compose version >/dev/null 2>&1; then
  COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE=(docker-compose)
else
  die "docker compose is not available (need Docker Compose v2, or docker-compose)."
fi

# Docker + firewall changes need root. Re-exec under sudo if we can.
if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then
    note "re-running with sudo (Docker + firewall need root)"
    exec sudo -E bash "$0" "$@"
  fi
  die "please run as root (sudo ./install.sh)."
fi

# ---- firewall ----------------------------------------------------------------

# Open the two ports on whichever firewall is active. Autodiscovery is UDP, so
# 7360/udp is the one everyone forgets.
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
  note "ufw is active — allowing 6969/tcp and 7360/udp"
  ufw allow 6969/tcp >/dev/null && note "  allowed 6969/tcp (web UI)"
  ufw allow 7360/udp >/dev/null && note "  allowed 7360/udp (autodiscovery)"
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
  note "firewalld is active — allowing 6969/tcp and 7360/udp"
  firewall-cmd --permanent --add-port=6969/tcp >/dev/null || warn "failed to add 6969/tcp"
  firewall-cmd --permanent --add-port=7360/udp >/dev/null || warn "failed to add 7360/udp"
  firewall-cmd --reload >/dev/null || warn "firewall-cmd reload failed"
else
  note "no active ufw/firewalld detected — assuming no host firewall to open"
fi

# ---- .env --------------------------------------------------------------------

if [ ! -f "${ENV_FILE}" ]; then
  [ -f "${ENV_EXAMPLE}" ] || die "missing ${ENV_EXAMPLE}; run from the repo root."
  cp "${ENV_EXAMPLE}" "${ENV_FILE}"
  note "created .env from .env.example — edit media paths + POSTGRES_PASSWORD, then re-run"
fi

# ---- boot --------------------------------------------------------------------

if [ "${DO_UP}" -eq 0 ]; then
  note "firewall done. Bring the stack up with:  ${COMPOSE[*]} up -d --build"
  exit 0
fi

note "building and starting the stack..."
( cd "${SCRIPT_DIR}" && "${COMPOSE[@]}" up -d --build )

HOST_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -n "${HOST_IP}" ] || HOST_IP="<this-machine-ip>"

cat <<EOF

Samo Server is up.

  Open:        http://${HOST_IP}:6969/
  Setup:       http://${HOST_IP}:6969/setup
  Logs:        ${COMPOSE[*]} logs -f server
  Stop:        ${COMPOSE[*]} down

  Autodiscovery is live on udp/7360 — LAN clients will find the server and get
  back http://${HOST_IP}:6969. Verify from another machine on the network:

    echo -n 'Who is SamoServer?' | nc -u -w1 ${HOST_IP} 7360
EOF
