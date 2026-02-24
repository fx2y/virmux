#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

db="$root/runs/virmux.sqlite"
skill=""
fmt="json"
limit=20

while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) db="$2"; shift 2 ;;
    --skill) skill="$2"; shift 2 ;;
    --fmt) fmt="$2"; shift 2 ;;
    --limit) limit="$2"; shift 2 ;;
    *) echo "usage: scripts/canary_report.sh [--db <path>] [--skill <name>] [--fmt json|one-line] [--limit <n>]" >&2; exit 1 ;;
  esac
done

[[ -f "$db" ]] || { echo "canary:report: missing db $db" >&2; exit 1; }
[[ "$fmt" == "json" || "$fmt" == "one-line" ]] || { echo "canary:report: fmt must be json|one-line" >&2; exit 1; }
[[ "$limit" =~ ^[0-9]+$ ]] || { echo "canary:report: limit must be integer" >&2; exit 1; }

where="1=1"
if [[ -n "$skill" ]]; then
  esc_skill="$(printf '%s' "$skill" | sed "s/'/''/g")"
  where="cr.skill='${esc_skill}'"
fi

rows="$(sqlite3 -json "$db" "
SELECT
  cr.id,
  cr.created_at,
  cr.skill,
  cr.eval_run_id,
  cr.curated_eval_run_id,
  cr.dset_path,
  cr.action,
  cr.action_ref,
  cr.caught_by_canary,
  er.pass,
  er.score_p50_delta,
  er.fail_rate_delta,
  er.cost_delta
FROM canary_runs cr
JOIN eval_runs er ON er.id = cr.eval_run_id
WHERE ${where}
ORDER BY datetime(cr.created_at) DESC, cr.id DESC
LIMIT ${limit};
")"

if [[ "$fmt" == "one-line" ]]; then
  jq -r '.[] | "ts=" + .created_at + " skill=" + .skill + " eval=" + .eval_run_id + " pass=" + ((.pass|tonumber|if .==1 then "true" else "false" end)) + " action=" + .action + " caught_by_canary=" + ((.caught_by_canary|tonumber)|tostring) + " fr_delta=" + (.fail_rate_delta|tostring) + " score_delta=" + (.score_p50_delta|tostring) + " cost_delta=" + (.cost_delta|tostring) + " dset=" + .dset_path' <<<"$rows"
  exit 0
fi

jq -S '{count:length,rows:.}' <<<"$rows"
