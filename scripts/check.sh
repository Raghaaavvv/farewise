#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/toolchain.sh"
cd "$PROJECT_ROOT/backend"
go test -race ./...
go vet ./...
cd "$PROJECT_ROOT/frontend"
pnpm test
pnpm typecheck
pnpm build
