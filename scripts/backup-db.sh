#!/usr/bin/env bash
# Save a private PostgreSQL backup of the real observation schema.
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Ensure the container is healthy before asking it to export the database.
bash "$PROJECT_ROOT/scripts/db.sh" up

if ! command -v docker >/dev/null 2>&1; then
  export PATH="/Applications/Docker.app/Contents/Resources/bin:$HOME/.docker/bin:$PATH"
fi

# Backups contain observation coordinates and remain readable only by the owner.
umask 077

mkdir -p "$PROJECT_ROOT/backups"

backup_path="$PROJECT_ROOT/backups/observations-$(date -u +%Y%m%dT%H%M%SZ).dump"

# Expand PostgreSQL credentials inside the container, not into command arguments.
docker compose \
  --project-directory "$PROJECT_ROOT" \
  --env-file "$PROJECT_ROOT/backend/db.env" \
  -f "$PROJECT_ROOT/compose.yaml" \
  exec -T db sh -c \
  'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --schema=public' \
  > "$backup_path"

echo "Saved private backup: $backup_path"
