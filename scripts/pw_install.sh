#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

export PLAYWRIGHT_BROWSERS_PATH="$root/.cache/ms-playwright"
npm ci
mode="with_deps"
if ! npx playwright install --with-deps chromium; then
  mode="browser_only"
  echo "pw:install: --with-deps failed; retrying browser-only install" >&2
  npx playwright install chromium
fi

mkdir -p "$root/tmp"
audit_json="$(npm audit --omit=dev --json 2>/dev/null || true)"
if [[ -z "$audit_json" ]]; then
  audit_json='{}'
fi
if ! printf '%s' "$audit_json" | jq -c --arg mode "$mode" '
  . as $a
  | ($a.metadata.vulnerabilities // {}) as $v
  | {
      installed_at: (now | todateiso8601),
      mode: $mode,
      audit_info: ($v.info // 0),
      audit_low: ($v.low // 0),
      audit_moderate: ($v.moderate // 0),
      audit_high: ($v.high // 0),
      audit_critical: ($v.critical // 0),
      audit_total: (($v.info // 0)+($v.low // 0)+($v.moderate // 0)+($v.high // 0)+($v.critical // 0))
    }
' > "$root/tmp/pw-install-status.json"; then
  jq -n --arg mode "$mode" --arg installed_at "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" '
    {installed_at:$installed_at,mode:$mode,audit_info:0,audit_low:0,audit_moderate:0,audit_high:0,audit_critical:0,audit_total:0}
  ' > "$root/tmp/pw-install-status.json"
fi

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/.cache/ms-playwright/.install-stamp"
