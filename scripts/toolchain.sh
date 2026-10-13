#!/usr/bin/env bash
# Resolve ordinary system tools first, then this Codex machine's local runtimes.
# This file is sourced by dev.sh and check.sh; it does not install software.
set -euo pipefail
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCAL_GO="$PROJECT_ROOT/../../work/toolchain/go/bin"
BUNDLED_RUNTIME="$HOME/.cache/codex-runtimes/codex-primary-runtime/dependencies"
if ! command -v go >/dev/null 2>&1 && [ -x "$LOCAL_GO/go" ]; then export PATH="$LOCAL_GO:$PATH"; fi
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
