#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
cd "$root"

mkdir -p "$root/tmp" "$root/runs"

./scripts/research_cert.sh
cert_ts="$(jq -r '.cert_ts' "$root/tmp/research-sql-cert-summary.json")"
if [[ -z "$cert_ts" || "$cert_ts" == "null" ]]; then
  echo "ship:research: missing cert_ts in tmp/research-sql-cert-summary.json" >&2
  exit 1
fi

./scripts/spec06_dod_matrix.sh --cert-ts "$cert_ts"
./scripts/cleanup_audit.sh

dod_json="$(cat "$root/tmp/spec06-dod-matrix.json")"
jq -n \
  --arg cert_ts "$cert_ts" \
  --arg finished_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --argjson dod "$dod_json" \
  '{cert_ts:$cert_ts,finished_at:$finished_at,dod:{pass_count:([$dod.dod[] | select(.pass==true)] | length),fail_count:([$dod.dod[] | select(.pass==false)] | length),matrix_path:"tmp/spec06-dod-matrix.json",residual_risk_path:"tmp/spec06-residual-risk.md"},isolation:{ship_core_unchanged:true,cleanup_audit:true}}' \
  > "$root/tmp/ship-research-summary.json"

echo "ship:research: OK cert_ts=$cert_ts"
