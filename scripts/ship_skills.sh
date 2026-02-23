#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

mkdir -p "$root/tmp" "$root/runs"
cert_ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
cohort="qa-skill-c3-$(date -u +%Y%m%dT%H%M%SZ)-$$"

mise run skill:lint
mise run skill:test:core
mise run skill:test:c2
mise run skill:test:c3

jq -n \
  --arg cert_ts "$cert_ts" \
  --arg cohort "$cohort" \
  --arg finished_at "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  '{cert_ts:$cert_ts,cohort:$cohort,finished_at:$finished_at,tasks:["skill:lint","skill:test:core","skill:test:c2","skill:test:c3"]}' \
  > "$root/tmp/ship-skills-summary.json"

echo "ship:skills: OK"
