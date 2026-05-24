#!/usr/bin/env bash
set -euo pipefail

SERVER_USER="jake"
SERVER_HOST="192.168.1.10"

TARBALL="dist/samo-server-linux-amd64.tar.gz"
REMOTE_DIR="/tmp/samo-server-deploy"
TARBALL_NAME="$(basename "$TARBALL")"

if [ ! -f "$TARBALL" ]; then
  echo "Missing tarball: $TARBALL"
  exit 1
fi

echo "Deploying $TARBALL to ${SERVER_USER}@${SERVER_HOST}..."

cat "$TARBALL" | ssh -t "${SERVER_USER}@${SERVER_HOST}" "
set -euo pipefail

rm -rf '${REMOTE_DIR}'
mkdir -p '${REMOTE_DIR}'
cd '${REMOTE_DIR}'

echo 'Receiving tarball...'
cat > '${TARBALL_NAME}'

echo 'Extracting...'
tar -xzf '${TARBALL_NAME}'

echo 'Installing...'
chmod +x ./install.sh
sudo ./install.sh

echo 'Install complete.'
"
