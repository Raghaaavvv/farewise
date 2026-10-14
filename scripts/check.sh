#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/toolchain.sh"
python3 -m unittest discover -s "$PROJECT_ROOT/scripts/tests"
cd "$PROJECT_ROOT/backend"
go test -race ./...
go vet ./...
cd "$PROJECT_ROOT/frontend"
pnpm test
pnpm typecheck
pnpm build
