#!/usr/bin/env bash
set -euo pipefail

REMOTE_USER="${REMOTE_USER:-jake}"
REMOTE_HOST="${REMOTE_HOST:-192.168.1.10}"
REMOTE_DIR="${REMOTE_DIR:-/tmp/samo-server-src}"
REMOTE_DATA_DIR="${REMOTE_DATA_DIR:-/tmp/samo-data}"

echo "==> Syncing samo-server to ${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}"

rsync -az --delete \
  --exclude ".git" \
  --exclude "dist" \
  --exclude "tmp" \
  --exclude "*.db" \
  ./ "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}/"

echo "==> Building and running samo-server on ${REMOTE_HOST}"

ssh -t "${REMOTE_USER}@${REMOTE_HOST}" "
  set -euo pipefail

  cd '${REMOTE_DIR}'

  echo '==> Building /tmp/samo'
  go build -o /tmp/samo ./cmd/samo-server

  echo '==> Starting samo-server'
  SAMO_FFMPEG_PATH=\$(which ffmpeg) \
  SAMO_FFPROBE_PATH=\$(which ffprobe) \
  SAMO_DATA_DIR='${REMOTE_DATA_DIR}' \
    /tmp/samo
"
