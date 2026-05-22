#!/usr/bin/env bash
set -euo pipefail

if [ $# -lt 1 ]; then
  echo "Usage: ./commit.sh \"Commit message\""
  exit 1
fi

MESSAGE="$*"

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "Error: not inside a git repository."
  exit 1
fi

echo "==> Formatting Go files..."
if command -v gofmt >/dev/null 2>&1; then
  gofmt -w $(find . -type f -name '*.go' -not -path './.git/*')
fi

echo "==> Running tests..."
go test ./...

echo "==> Running vet..."
go vet ./...

echo "==> Git status:"
git status --short

if [ -z "$(git status --short)" ]; then
  echo "Nothing to commit."
  exit 0
fi

echo "==> Staging changes..."
git add .

echo "==> Committing:"
echo "    $MESSAGE"
git commit -m "$MESSAGE"

echo "==> Done."
