#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

db="$root/runs/virmux.sqlite"
cert_ts=""
out_json="$root/tmp/spec05-dod-matrix.json"
risk_md="$root/tmp/spec05-residual-risk.md"

usage() {
  echo "usage: scripts/spec05_dod_matrix.sh [--db <path>] [--cert-ts <RFC3339>] [--out <path>] [--risk-out <path>]" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) db="$2"; shift 2 ;;
    --cert-ts) cert_ts="$2"; shift 2 ;;
    --out) out_json="$2"; shift 2 ;;
    --risk-out) risk_md="$2"; shift 2 ;;
    *) usage ;;
  esac
done

[[ -f "$db" ]] || { echo "spec05:dod: missing db $db" >&2; exit 1; }
[[ -f "$root/tmp/skill-sql-cert-summary.json" ]] || { echo "spec05:dod: missing tmp/skill-sql-cert-summary.json" >&2; exit 1; }

fresh_filter=""
fresh_filter_er=""
if [[ -n "$cert_ts" ]]; then
  fresh_filter=" AND datetime(created_at) >= datetime('${cert_ts}')"
  fresh_filter_er=" AND datetime(er.created_at) >= datetime('${cert_ts}')"
fi

c3_eval_total="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE 'qa-skill-c3-%'${fresh_filter};")"
c3_eval_pass="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE 'qa-skill-c3-%' AND pass=1${fresh_filter};")"
c3_eval_fail="$(sqlite3 "$db" "SELECT COUNT(*) FROM eval_runs WHERE cohort LIKE 'qa-skill-c3-%' AND pass=0${fresh_filter};")"
exp_rows="$(sqlite3 "$db" "SELECT COUNT(*) FROM experiments e JOIN eval_runs er ON er.id=e.eval_run_id WHERE er.cohort LIKE 'qa-skill-c3-%'${fresh_filter_er};")"
comp_rows="$(sqlite3 "$db" "SELECT COUNT(*) FROM comparisons c JOIN experiments e ON e.id=c.experiment_id JOIN eval_runs er ON er.id=e.eval_run_id WHERE er.cohort LIKE 'qa-skill-c3-%'${fresh_filter_er};")"
promo_rows="$(sqlite3 "$db" "SELECT COUNT(*) FROM promotions WHERE eval_run_id IN (SELECT id FROM eval_runs WHERE (cohort LIKE 'qa-skill-c3-%' OR cohort LIKE 'qa-skill-c5-%')${fresh_filter});")"
rollback_rows="$(sqlite3 "$db" "SELECT COUNT(*) FROM promotions WHERE op='rollback' AND eval_run_id IN (SELECT id FROM eval_runs WHERE cohort LIKE 'qa-skill-c5-%'${fresh_filter});")"
canary_rows="$(sqlite3 "$db" "SELECT COUNT(*) FROM canary_runs WHERE eval_run_id IN (SELECT id FROM eval_runs WHERE cohort LIKE 'qa-skill-c5-%'${fresh_filter});")"
canary_action_rows="$(sqlite3 "$db" "SELECT COUNT(*) FROM canary_runs WHERE eval_run_id IN (SELECT id FROM eval_runs WHERE cohort LIKE 'qa-skill-c5-%'${fresh_filter}) AND action IN ('promote','rollback');")"

judge_guard=0
[[ -f "$root/tmp/skill-test-c2.ok" ]] && judge_guard=1

eval_run_ok=0
[[ "$c3_eval_total" -ge 2 && "$c3_eval_pass" -ge 1 && "$c3_eval_fail" -ge 1 ]] && eval_run_ok=1

ab_ok=0
[[ "$exp_rows" -ge 1 && "$comp_rows" -ge 1 ]] && ab_ok=1

promo_ok=0
[[ "$promo_rows" -ge 2 && "$rollback_rows" -ge 1 ]] && promo_ok=1

canary_ok=0
[[ "$canary_rows" -ge 2 && "$canary_action_rows" -ge 2 && "$rollback_rows" -ge 1 ]] && canary_ok=1

mkdir -p "$(dirname "$out_json")" "$(dirname "$risk_md")"

jq -n -S \
  --arg cert_ts "$cert_ts" \
  --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --argjson c3_eval_total "$c3_eval_total" \
  --argjson c3_eval_pass "$c3_eval_pass" \
  --argjson c3_eval_fail "$c3_eval_fail" \
  --argjson exp_rows "$exp_rows" \
  --argjson comp_rows "$comp_rows" \
  --argjson promo_rows "$promo_rows" \
  --argjson rollback_rows "$rollback_rows" \
  --argjson canary_rows "$canary_rows" \
  --argjson canary_action_rows "$canary_action_rows" \
  --argjson eval_run_ok "$eval_run_ok" \
  --argjson judge_guard "$judge_guard" \
  --argjson ab_ok "$ab_ok" \
  --argjson promo_ok "$promo_ok" \
  --argjson canary_ok "$canary_ok" \
  '{cert_ts:$cert_ts,generated_at:$generated_at,dod:[
    {id:"DOD-1",spec_line:549,canon:"virmux skill run + deterministic scoring evidence",pass:($eval_run_ok==1),proof:["tmp/skill-test-c3.ok","tmp/skill-sql-cert-summary.json"],metrics:{eval_total:$c3_eval_total,eval_pass:$c3_eval_pass,eval_fail:$c3_eval_fail}},
    {id:"DOD-2",spec_line:550,canon:"virmux skill judge schema-valid + reproducible guard",pass:($judge_guard==1),proof:["tmp/skill-test-c2.ok","cmd/virmux/skill_test.go"]},
    {id:"DOD-3",spec_line:551,canon:"virmux skill ab pairwise rows + reportable metrics",pass:($ab_ok==1),proof:["tmp/skill-test-c3.ok","runs/virmux.sqlite:experiments,comparisons"],metrics:{experiments:$exp_rows,comparisons:$comp_rows}},
    {id:"DOD-4",spec_line:552,canon:"virmux skill promote --rollback audit trail",pass:($promo_ok==1),proof:["tmp/rollback-playbook-smoke.ok","runs/virmux.sqlite:promotions"],metrics:{promotions:$promo_rows,rollback:$rollback_rows}},
    {id:"DOD-5",spec_line:553,canon:"canary loop blocks regressions with rollback action",pass:($canary_ok==1),proof:["tmp/skill-canary-cert.ok","runs/virmux.sqlite:canary_runs"],metrics:{canary_rows:$canary_rows,canary_action_rows:$canary_action_rows,rollback:$rollback_rows}}
  ]}' > "$out_json"

fail_count="$(jq '[.dod[] | select(.pass==false)] | length' "$out_json")"
if [[ "$fail_count" -gt 0 ]]; then
  {
    echo "# Residual Risks (C7)"
    echo
    echo "- RISK-C7-001: One or more DoD cells missing executable evidence."
    echo "- Owner: skill-lane"
    echo "- Action: block cutover; rerun `mise run ship:skills`; inspect tmp/spec05-dod-matrix.json"
  } > "$risk_md"
  echo "spec05:dod: failed with $fail_count unchecked DoD cells" >&2
  exit 1
fi

{
  echo "# Residual Risks (C7)"
  echo
  echo "- none (all DoD cells passed with executable evidence)"
} > "$risk_md"

echo "spec05:dod: OK"
