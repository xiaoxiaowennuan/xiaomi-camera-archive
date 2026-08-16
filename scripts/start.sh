#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose() {
  docker compose --env-file "$repo_dir/deploy/.env" -f "$repo_dir/deploy/compose.yaml" "$@"
}

if [ ! -f "$repo_dir/deploy/.env" ]; then
  echo "Missing deploy/.env. Copy deploy/.env.example and configure it first." >&2
  exit 1
fi

if [ "${1:-}" = "--build" ]; then
  compose build
fi

compose up -d --no-build
