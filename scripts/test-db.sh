#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/toolchain.sh"
bash "$PROJECT_ROOT/scripts/db.sh" up
set -a
source "$PROJECT_ROOT/backend/db.env"
set +a
cd "$PROJECT_ROOT/backend"
TEST_DATABASE_URL="$DATABASE_URL" go test -race ./internal/observations -run TestPostgres -count=1
