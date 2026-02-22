#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

export PLAYWRIGHT_BROWSERS_PATH="$root/.cache/ms-playwright"
npm ci
if ! npx playwright install --with-deps chromium; then
  echo "pw:install: --with-deps failed; retrying browser-only install" >&2
  npx playwright install chromium
fi

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/.cache/ms-playwright/.install-stamp"
