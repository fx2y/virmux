#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

mkdir -p "$root/tmp" "$root/runs"
cert_ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
cert_tag="qa-skill-c7-$(date -u +%Y%m%dT%H%M%SZ)-$$"
export SKILL_CERT_TS="$cert_ts"

go run ./cmd/virmux skill lint skills/dd >/dev/null
./scripts/skill_test_core.sh
./scripts/skill_test_c2.sh
./scripts/skill_test_c3.sh
./scripts/skill_test_c4.sh
./scripts/skill_test_c5.sh
./scripts/skill_test_c6.sh
./scripts/skill_test_c7.sh

./scripts/cleanup_audit.sh

sql_cert_json="$(cat "$root/tmp/skill-sql-cert-summary.json")"
dod_json="$(cat "$root/tmp/spec05-dod-matrix.json")"

jq -n \
  --arg cert_ts "$cert_ts" \
  --arg cert_tag "$cert_tag" \
  --argjson sql "$sql_cert_json" \
  --argjson dod "$dod_json" \
  --arg finished_at "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  '{cert_ts:$cert_ts,cert_tag:$cert_tag,finished_at:$finished_at,tasks:["skill:lint","skill:test:core","skill:test:c2","skill:test:c3","skill:test:c4","skill:test:c5","skill:test:c6","skill:test:c7"],freshness:{eval_total:$sql.eval_total,eval_pass:$sql.eval_pass,eval_fail:$sql.eval_fail,promotions_promote:$sql.promotions_promote,promotions_rollback:$sql.promotions_rollback,canary_rows:$sql.canary_rows,canary_action_rows:$sql.canary_action_rows},dod:{pass_count:([$dod.dod[] | select(.pass==true)] | length),fail_count:([$dod.dod[] | select(.pass==false)] | length),matrix_path:"tmp/spec05-dod-matrix.json",residual_risk_path:"tmp/spec05-residual-risk.md"},isolation:{ship_core_unchanged:true,cleanup_audit:true}}' \
  > "$root/tmp/ship-skills-summary.json"

echo "ship:skills: OK cert_tag=$cert_tag"
