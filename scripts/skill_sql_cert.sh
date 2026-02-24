#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
db="$root/runs/virmux.sqlite"
if [[ ! -f "$db" ]]; then
  echo "skill:sql-cert: missing db $db" >&2
  exit 1
fi

cohort_glob='qa-skill-c3-%'
canary_glob='qa-skill-c5-%'
rows_total="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE '$cohort_glob';")"
rows_pass="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE '$cohort_glob' AND pass=1;")"
rows_fail="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE '$cohort_glob' AND pass=0;")"
rows_promo="$(sqlite3 "$db" "SELECT COUNT(*) FROM promotions WHERE eval_run_id IN (SELECT id FROM eval_runs WHERE cohort LIKE '$cohort_glob' OR cohort LIKE '$canary_glob');")"
rows_exp="$(sqlite3 "$db" "SELECT COUNT(*) FROM experiments WHERE id IN (SELECT experiment_id FROM comparisons);")"
rows_comp="$(sqlite3 "$db" "SELECT COUNT(*) FROM comparisons WHERE experiment_id IN (SELECT id FROM experiments);")"
rows_canary="$(sqlite3 "$db" "SELECT COUNT(*) FROM canary_runs WHERE eval_run_id IN (SELECT id FROM eval_runs WHERE cohort LIKE '$canary_glob');")"
rows_canary_action="$(sqlite3 "$db" "SELECT COUNT(*) FROM canary_runs WHERE eval_run_id IN (SELECT id FROM eval_runs WHERE cohort LIKE '$canary_glob') AND action IN ('promote','rollback');")"

[[ "$rows_total" -ge 2 ]] || { echo "skill:sql-cert: expected >=2 eval_runs for cohort=$cohort_glob" >&2; exit 1; }
[[ "$rows_pass" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 passing eval_run" >&2; exit 1; }
[[ "$rows_fail" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 failing eval_run" >&2; exit 1; }
[[ "$rows_promo" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 promotion row" >&2; exit 1; }
[[ "$rows_exp" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 experiment row" >&2; exit 1; }
[[ "$rows_comp" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 comparison row" >&2; exit 1; }
[[ "$rows_canary" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 canary row for cohort=$canary_glob" >&2; exit 1; }
[[ "$rows_canary_action" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 canary action row (promote|rollback)" >&2; exit 1; }

mkdir -p "$root/tmp"
jq -n --arg total "$rows_total" --arg pass "$rows_pass" --arg fail "$rows_fail" --arg promo "$rows_promo" --arg exp "$rows_exp" --arg comp "$rows_comp" --arg canary "$rows_canary" --arg canary_action "$rows_canary_action" '{eval_total:($total|tonumber),eval_pass:($pass|tonumber),eval_fail:($fail|tonumber),promotions:($promo|tonumber),experiments:($exp|tonumber),comparisons:($comp|tonumber),canary_rows:($canary|tonumber),canary_action_rows:($canary_action|tonumber)}' > "$root/tmp/skill-sql-cert-summary.json"
date -u +%FT%TZ > "$root/tmp/skill-sql-cert.ok"
echo "skill:sql-cert: OK"
