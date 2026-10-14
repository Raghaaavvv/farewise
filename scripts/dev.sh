#!/usr/bin/env bash
# Start the local database, Go API and Next.js development server.
set -euo pipefail

source "$(dirname "$0")/toolchain.sh"

# Reuse existing database settings; db.sh preserves the database volume.
if [ -f "$PROJECT_ROOT/backend/db.env" ]; then
  bash "$PROJECT_ROOT/scripts/db.sh" up
fi

cd "$PROJECT_ROOT/frontend"

if [ ! -d node_modules ]; then
  pnpm install --frozen-lockfile
fi

# Stop the API process when the foreground frontend exits or is interrupted.
cleanup() {
  trap - EXIT INT TERM

  if [ -n "${API_PID:-}" ]; then
    kill "$API_PID" 2>/dev/null || true
    wait "$API_PID" 2>/dev/null || true
  fi
}

trap cleanup EXIT
trap 'exit 130' INT TERM

# Load private backend settings without including them in browser code.
cd "$PROJECT_ROOT/backend"

if [ -f db.env ]; then
  set -a
  source db.env
  set +a
fi

if [ -f .env ]; then
  set -a
  source .env
  set +a
fi

# Run the built binary directly so cleanup stops the actual API process.
mkdir -p bin

go build -o bin/farewise ./cmd/server

bin/farewise &
API_PID=$!

sleep 0.5

if ! kill -0 "$API_PID" 2>/dev/null; then
  echo 'The API could not start. Check that port 8080 is free.'
  exit 1
fi

# Keep Next.js in the foreground; forward any requested development options.
cd "$PROJECT_ROOT/frontend"

echo 'Farewise: http://localhost:3000 — all fares are simulated. Ctrl+C stops both servers.'

pnpm dev "$@"
