#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

mkdir -p "$root/tmp" "$root/runs"
cert_ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
cert_tag="qa-skill-c6-$(date -u +%Y%m%dT%H%M%SZ)-$$"

go run ./cmd/virmux skill lint skills/dd >/dev/null
./scripts/skill_test_core.sh
./scripts/skill_test_c2.sh
./scripts/skill_test_c3.sh
./scripts/skill_test_c4.sh
./scripts/skill_test_c5.sh
./scripts/skill_test_c6.sh

./scripts/cleanup_audit.sh

fresh_eval_count="$(sqlite3 "$root/runs/virmux.sqlite" "
SELECT COUNT(*)
FROM eval_runs
WHERE datetime(created_at) >= datetime('$cert_ts')
  AND cohort LIKE 'qa-skill-c3-%';")"
if [[ "$fresh_eval_count" -lt 2 ]]; then
  echo "ship:skills: expected >=2 fresh eval_runs since cert_ts=$cert_ts (pass+fail AB), got $fresh_eval_count" >&2
  exit 1
fi

jq -n \
  --arg cert_ts "$cert_ts" \
  --arg cert_tag "$cert_tag" \
  --arg fresh_eval_count "$fresh_eval_count" \
  --arg finished_at "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  '{cert_ts:$cert_ts,cert_tag:$cert_tag,finished_at:$finished_at,fresh_eval_count:($fresh_eval_count|tonumber),tasks:["skill:lint","skill:test:core","skill:test:c2","skill:test:c3","skill:test:c4","skill:test:c5","skill:test:c6"],isolation:{ship_core_unchanged:true,cleanup_audit:true}}' \
  > "$root/tmp/ship-skills-summary.json"

echo "ship:skills: OK cert_tag=$cert_tag"
