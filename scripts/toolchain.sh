#!/usr/bin/env bash
# Resolve ordinary system tools first, then this Codex machine's local runtimes.
# This file is sourced by dev.sh and check.sh; it does not install software.
set -euo pipefail
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCAL_GO="$PROJECT_ROOT/../../work/toolchain/go/bin"
BUNDLED_RUNTIME="$HOME/.cache/codex-runtimes/codex-primary-runtime/dependencies"
# Standard installations can exist even when a GUI terminal has a minimal PATH.
if ! command -v go >/dev/null 2>&1; then
  for go_bin in /opt/homebrew/bin /usr/local/go/bin /usr/local/bin "$LOCAL_GO"; do
    if [ -x "$go_bin/go" ]; then export PATH="$go_bin:$PATH"; break; fi
  done
fi
if ! command -v node >/dev/null 2>&1 && [ -x "$BUNDLED_RUNTIME/node/bin/node" ]; then export PATH="$BUNDLED_RUNTIME/node/bin:$PATH"; fi
if ! command -v pnpm >/dev/null 2>&1 && [ -x "$BUNDLED_RUNTIME/bin/fallback/pnpm" ]; then export PATH="$BUNDLED_RUNTIME/bin/fallback:$PATH"; fi
for tool in go node pnpm; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "Missing $tool. See README.md → Setup on another machine."
    exit 1
  fi
done
export GOCACHE="${TMPDIR:-/tmp}/farewise-go-cache"
export GOTOOLCHAIN=local
export NEXT_TELEMETRY_DISABLED=1
export WATCHPACK_POLLING=1000

# Check the SDK before building so unreadable standard-library files produce
# one actionable startup error rather than a cascade of missing-package errors.
if ! GOWORK=off GO111MODULE=on go list context encoding/json >/dev/null 2>&1; then
  echo "Go cannot load its standard library from: $(go env GOROOT)" >&2
  echo 'Check this terminal’s Go installation: https://go.dev/doc/install.' >&2
  GOWORK=off GO111MODULE=on go list context encoding/json >&2 || true
  exit 1
fi
