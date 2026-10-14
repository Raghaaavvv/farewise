#!/usr/bin/env bash
# Run the existing checks for setup scripts, backend and frontend.
set -euo pipefail

source "$(dirname "$0")/toolchain.sh"

# Setup tests never need real OneMap credentials.
python3 -m unittest discover -s "$PROJECT_ROOT/scripts/tests"

# Check concurrent backend behavior and Go static analysis.
cd "$PROJECT_ROOT/backend"

go test -race ./...
go vet ./...

# Check runtime contracts, TypeScript types and the production build.
cd "$PROJECT_ROOT/frontend"

pnpm test
pnpm typecheck
pnpm build
