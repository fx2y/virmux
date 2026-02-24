#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
db="$root/runs/virmux.sqlite"
if [[ ! -f "$db" ]]; then
  echo "skill:sql-cert: missing db $db" >&2
  exit 1
fi

cohort_glob='qa-skill-c3-%'
rows_total="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE '$cohort_glob';")"
rows_pass="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE '$cohort_glob' AND pass=1;")"
rows_fail="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE '$cohort_glob' AND pass=0;")"
rows_promo="$(sqlite3 "$db" "SELECT COUNT(*) FROM promotions WHERE eval_run_id IN (SELECT id FROM eval_runs WHERE cohort LIKE '$cohort_glob');")"
rows_exp="$(sqlite3 "$db" "SELECT COUNT(*) FROM experiments WHERE id IN (SELECT experiment_id FROM comparisons);")"
rows_comp="$(sqlite3 "$db" "SELECT COUNT(*) FROM comparisons WHERE experiment_id IN (SELECT id FROM experiments);")"

[[ "$rows_total" -ge 2 ]] || { echo "skill:sql-cert: expected >=2 eval_runs for cohort=$cohort_glob" >&2; exit 1; }
[[ "$rows_pass" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 passing eval_run" >&2; exit 1; }
[[ "$rows_fail" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 failing eval_run" >&2; exit 1; }
[[ "$rows_promo" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 promotion row" >&2; exit 1; }
[[ "$rows_exp" -ge 1 ]] || { echo "skill:sql-cert: expected >=1 experiment row" >&2; exit 1; }

mkdir -p "$root/tmp"
jq -n --arg total "$rows_total" --arg pass "$rows_pass" --arg fail "$rows_fail" --arg promo "$rows_promo" --arg exp "$rows_exp" --arg comp "$rows_comp" '{eval_total:($total|tonumber),eval_pass:($pass|tonumber),eval_fail:($fail|tonumber),promotions:($promo|tonumber),experiments:($exp|tonumber),comparisons:($comp|tonumber)}' > "$root/tmp/skill-sql-cert-summary.json"
date -u +%FT%TZ > "$root/tmp/skill-sql-cert.ok"
echo "skill:sql-cert: OK"
