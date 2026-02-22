#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"
export PLAYWRIGHT_BROWSERS_PATH="$root/.cache/ms-playwright"
node scripts/pw_smoke.mjs

mkdir -p "$root/tmp"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/pw-smoke.ok"
