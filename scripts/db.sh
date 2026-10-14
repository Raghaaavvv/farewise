#!/usr/bin/env bash
set -euo pipefail
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if ! command -v docker >/dev/null 2>&1; then
  for docker_bin in "$HOME/.docker/bin" /Applications/Docker.app/Contents/Resources/bin /opt/homebrew/bin /usr/local/bin; do
    if [ -x "$docker_bin/docker" ]; then export PATH="$docker_bin:$PATH"; break; fi
  done
fi
if ! command -v docker >/dev/null 2>&1; then echo 'Install and start Docker Desktop, then retry.' >&2; exit 1; fi
if ! docker info >/dev/null 2>&1; then echo 'Start Docker Desktop and wait for its engine, then retry.' >&2; exit 1; fi
python3 "$PROJECT_ROOT/scripts/setup-db.py"
compose=(docker compose --project-directory "$PROJECT_ROOT" --env-file "$PROJECT_ROOT/backend/db.env" -f "$PROJECT_ROOT/compose.yaml")
case "${1:-up}" in
  up) "${compose[@]}" up -d --wait db ;;
  stop) "${compose[@]}" stop db ;;
  status) "${compose[@]}" ps ;;
  shell) "${compose[@]}" exec db sh -c 'exec psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' ;;
  *) echo 'Usage: bash scripts/db.sh [up|stop|status|shell]' >&2; exit 1 ;;
esac
