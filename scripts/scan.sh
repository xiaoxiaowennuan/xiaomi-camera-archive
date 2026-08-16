#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
docker compose --env-file "$repo_dir/deploy/.env" -f "$repo_dir/deploy/compose.yaml" run --rm --no-deps mijia-archive scan
