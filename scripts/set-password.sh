#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
username=${1:-admin}

if [ ! -f "$repo_dir/deploy/.env" ]; then
  echo "Missing deploy/.env. Copy deploy/.env.example and configure it first." >&2
  exit 1
fi

compose() {
  docker compose --env-file "$repo_dir/deploy/.env" -f "$repo_dir/deploy/compose.yaml" "$@"
}

compose stop mijia-archive
status=0
compose run --rm --no-deps mijia-archive set-password \
  --username "$username" \
  --password-file /run/secrets/bootstrap_admin_password || status=$?
compose up -d --no-build mijia-archive
exit "$status"
