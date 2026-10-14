#!/usr/bin/env bash
# Run integration tests against isolated schemas in the local PostgreSQL database.
set -euo pipefail

source "$(dirname "$0")/toolchain.sh"

bash "$PROJECT_ROOT/scripts/db.sh" up

# Export the connection settings only to the backend test process.
set -a
source "$PROJECT_ROOT/backend/db.env"
set +a

cd "$PROJECT_ROOT/backend"

TEST_DATABASE_URL="$DATABASE_URL" go test -race ./internal/observations -run TestPostgres -count=1
