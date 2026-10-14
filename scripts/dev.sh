#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/toolchain.sh"
cd "$PROJECT_ROOT/frontend"
if [ ! -d node_modules ]; then pnpm install --frozen-lockfile; fi
cleanup() {
  trap - EXIT INT TERM
  if [ -n "${API_PID:-}" ]; then kill "$API_PID" 2>/dev/null || true; wait "$API_PID" 2>/dev/null || true; fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM
# Run the built binary directly, so stopping this script stops the actual API.
cd "$PROJECT_ROOT/backend"
if [ -f .env ]; then
  set -a
  source .env
  set +a
fi
mkdir -p bin
go build -o bin/farewise ./cmd/server
bin/farewise &
API_PID=$!
sleep 0.5
if ! kill -0 "$API_PID" 2>/dev/null; then echo 'The API could not start. Check that port 8080 is free.'; exit 1; fi
cd "$PROJECT_ROOT/frontend"
echo 'Farewise: http://localhost:3000 — all fares are simulated. Ctrl+C stops both servers.'
pnpm dev "$@"
