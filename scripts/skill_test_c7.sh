#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

cert_ts="${SKILL_CERT_TS:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

db="$root/runs/virmux.sqlite"
fresh_c3=0
if [[ -f "$db" ]]; then
  fresh_c3="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE 'qa-skill-c3-%' AND datetime(created_at) >= datetime('$cert_ts');" 2>/dev/null || echo 0)"
fi
if [[ "$fresh_c3" -lt 2 ]]; then
  ./scripts/skill_ab.sh
fi

./scripts/skill_canary_cert.sh
./scripts/skill_sql_cert.sh --cert-ts "$cert_ts" --require-canary
./scripts/spec05_dod_matrix.sh --cert-ts "$cert_ts"
./scripts/rollback_playbook_smoke.sh

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/skill-test-c7.ok"
echo "skill:test:c7: OK cert_ts=$cert_ts"
