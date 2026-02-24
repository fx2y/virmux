#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
db="$root/runs/virmux.sqlite"
cohort_c3_glob='qa-skill-c3-%'
cohort_c5_glob='qa-skill-c5-%'
cert_ts=""
require_canary=0

usage() {
  echo "usage: scripts/skill_sql_cert.sh [--db <path>] [--cohort-c3-glob <glob>] [--cohort-c5-glob <glob>] [--cert-ts <RFC3339>] [--require-canary]" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) db="$2"; shift 2 ;;
    --cohort-c3-glob) cohort_c3_glob="$2"; shift 2 ;;
    --cohort-c5-glob) cohort_c5_glob="$2"; shift 2 ;;
    --cert-ts) cert_ts="$2"; shift 2 ;;
    --require-canary) require_canary=1; shift ;;
    *) usage ;;
  esac
done

if [[ ! -f "$db" ]]; then
  echo "skill:sql-cert: missing db $db" >&2
  exit 1
fi

fresh_filter=""
if [[ -n "$cert_ts" ]]; then
  fresh_filter=" AND datetime(created_at) >= datetime('${cert_ts}')"
fi

rows_total="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE '$cohort_c3_glob'${fresh_filter};")"
rows_pass="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE '$cohort_c3_glob' AND pass=1${fresh_filter};")"
rows_fail="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE '$cohort_c3_glob' AND pass=0${fresh_filter};")"
rows_exp="$(sqlite3 "$db" "SELECT COUNT(*) FROM experiments WHERE skill='dd'${fresh_filter};")"
rows_comp="$(sqlite3 "$db" "SELECT COUNT(*) FROM comparisons WHERE experiment_id IN (SELECT id FROM experiments WHERE skill='dd'${fresh_filter});")"

rows_promo_total="$(sqlite3 "$db" "SELECT COUNT(*) FROM promotions WHERE eval_run_id IN (SELECT id FROM eval_runs WHERE (cohort LIKE '$cohort_c3_glob' OR cohort LIKE '$cohort_c5_glob')${fresh_filter});")"
rows_promo_promote="$(sqlite3 "$db" "SELECT COUNT(*) FROM promotions WHERE op='promote' AND eval_run_id IN (SELECT id FROM eval_runs WHERE (cohort LIKE '$cohort_c3_glob' OR cohort LIKE '$cohort_c5_glob')${fresh_filter});")"
rows_promo_rollback="$(sqlite3 "$db" "SELECT COUNT(*) FROM promotions WHERE op='rollback' AND eval_run_id IN (SELECT id FROM eval_runs WHERE (cohort LIKE '$cohort_c3_glob' OR cohort LIKE '$cohort_c5_glob')${fresh_filter});")"

rows_canary="$(sqlite3 "$db" "SELECT COUNT(*) FROM canary_runs WHERE eval_run_id IN (SELECT id FROM eval_runs WHERE cohort LIKE '$cohort_c5_glob'${fresh_filter});")"
rows_canary_action="$(sqlite3 "$db" "SELECT COUNT(*) FROM canary_runs WHERE eval_run_id IN (SELECT id FROM eval_runs WHERE cohort LIKE '$cohort_c5_glob'${fresh_filter}) AND action IN ('promote','rollback');")"
rows_canary_caught="$(sqlite3 "$db" "SELECT COUNT(*) FROM canary_runs WHERE eval_run_id IN (SELECT id FROM eval_runs WHERE cohort LIKE '$cohort_c5_glob'${fresh_filter}) AND caught_by_canary=1;")"

[[ "$rows_total" -ge 2 ]] || { echo "skill:sql-cert: expected >=2 eval_runs for cohort=$cohort_c3_glob" >&2; exit 1; }
[[ "$rows_pass" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 passing eval_run" >&2; exit 1; }
[[ "$rows_fail" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 failing eval_run" >&2; exit 1; }
[[ "$rows_promo_total" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 promotion row" >&2; exit 1; }
[[ "$rows_exp" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 experiment row" >&2; exit 1; }
[[ "$rows_comp" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 comparison row" >&2; exit 1; }

if [[ "$require_canary" -eq 1 ]]; then
  [[ "$rows_canary" -ge 2 ]] || { echo "skill:sql-cert: expected >=2 canary rows for cohort=$cohort_c5_glob" >&2; exit 1; }
  [[ "$rows_canary_action" -ge 2 ]] || { echo "skill:sql-cert: expected >=2 canary action rows (promote+rollback)" >&2; exit 1; }
  [[ "$rows_promo_promote" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 promote row in cert window" >&2; exit 1; }
  [[ "$rows_promo_rollback" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 rollback row in cert window" >&2; exit 1; }
  [[ "$rows_canary_caught" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 caught_by_canary row" >&2; exit 1; }
fi

mkdir -p "$root/tmp"
jq -n \
  --arg cert_ts "$cert_ts" \
  --arg c3 "$cohort_c3_glob" \
  --arg c5 "$cohort_c5_glob" \
  --arg req_canary "$require_canary" \
  --arg total "$rows_total" \
  --arg pass "$rows_pass" \
  --arg fail "$rows_fail" \
  --arg promo_total "$rows_promo_total" \
  --arg promo_promote "$rows_promo_promote" \
  --arg promo_rollback "$rows_promo_rollback" \
  --arg exp "$rows_exp" \
  --arg comp "$rows_comp" \
  --arg canary "$rows_canary" \
  --arg canary_action "$rows_canary_action" \
  --arg canary_caught "$rows_canary_caught" \
  '{cert_ts:$cert_ts,cohort_c3_glob:$c3,cohort_c5_glob:$c5,require_canary:($req_canary|tonumber),eval_total:($total|tonumber),eval_pass:($pass|tonumber),eval_fail:($fail|tonumber),promotions_total:($promo_total|tonumber),promotions_promote:($promo_promote|tonumber),promotions_rollback:($promo_rollback|tonumber),experiments:($exp|tonumber),comparisons:($comp|tonumber),canary_rows:($canary|tonumber),canary_action_rows:($canary_action|tonumber),canary_caught_rows:($canary_caught|tonumber)}' \
  > "$root/tmp/skill-sql-cert-summary.json"
date -u +%FT%TZ > "$root/tmp/skill-sql-cert.ok"
echo "skill:sql-cert: OK"
