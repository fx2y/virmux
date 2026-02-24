#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
db="$root/runs/virmux.sqlite"
label_glob='research-cert-%'
cert_ts=""
cert_id=""

usage() {
  echo "usage: scripts/research_sql_cert.sh [--db <path>] [--label-glob <glob>] [--cert-ts <RFC3339>] [--cert-id <id>]" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) db="$2"; shift 2 ;;
    --label-glob) label_glob="$2"; shift 2 ;;
    --cert-ts) cert_ts="$2"; shift 2 ;;
    --cert-id) cert_id="$2"; shift 2 ;;
    *) usage ;;
  esac
done

if [[ ! -f "$db" ]]; then
  echo "research:sql-cert: missing db $db" >&2
  exit 1
fi

fresh_filter=""
if [[ -n "$cert_ts" ]]; then
  fresh_filter=" AND datetime(started_at) >= datetime('${cert_ts}')"
fi

rows_run="$(sqlite3 "$db" "SELECT COUNT(*) FROM runs WHERE task='research:run' AND label LIKE '$label_glob'${fresh_filter};")"
rows_reduce="$(sqlite3 "$db" "SELECT COUNT(*) FROM runs WHERE task='research:reduce' AND label LIKE '$label_glob'${fresh_filter};")"
rows_replay="$(sqlite3 "$db" "SELECT COUNT(*) FROM runs WHERE task='research:replay' AND label LIKE '$label_glob'${fresh_filter};")"

# Evidence and artifacts for research runs in the cohort
rows_evidence="$(sqlite3 "$db" "SELECT COUNT(*) FROM evidence WHERE run_id IN (SELECT id FROM runs WHERE task='research:run' AND label LIKE '$label_glob'${fresh_filter});")"
rows_artifacts="$(sqlite3 "$db" "SELECT COUNT(*) FROM artifacts WHERE run_id IN (SELECT id FROM runs WHERE task LIKE 'research:%' AND label LIKE '$label_glob'${fresh_filter});")"

# Check for specific artifacts like report.md in any research task in the cohort
rows_reports="$(sqlite3 "$db" "SELECT COUNT(*) FROM artifacts WHERE path LIKE '%reduce/report.md' AND run_id IN (SELECT id FROM runs WHERE task LIKE 'research:%' AND label LIKE '$label_glob'${fresh_filter});")"

[[ "$rows_run" -ge 1 ]] || { echo "research:sql-cert: expected >=1 research:run for label=$label_glob" >&2; exit 1; }
[[ "$rows_reduce" -ge 1 ]] || { echo "research:sql-cert: expected >=1 research:reduce for label=$label_glob" >&2; exit 1; }
[[ "$rows_replay" -ge 1 ]] || { echo "research:sql-cert: expected >=1 research:replay for label=$label_glob" >&2; exit 1; }
[[ "$rows_evidence" -ge 1 ]] || { echo "research:sql-cert: expected >=1 evidence row" >&2; exit 1; }
[[ "$rows_artifacts" -ge 3 ]] || { echo "research:sql-cert: expected >=3 artifact rows" >&2; exit 1; }
[[ "$rows_reports" -ge 1 ]] || { echo "research:sql-cert: expected >=1 report.md artifact" >&2; exit 1; }

mkdir -p "$root/tmp"
jq -n \
  --arg cert_ts "$cert_ts" \
  --arg label "$label_glob" \
  --arg run "$rows_run" \
  --arg reduce "$rows_reduce" \
  --arg replay "$rows_replay" \
  --arg evidence "$rows_evidence" \
  --arg artifacts "$rows_artifacts" \
  --arg reports "$rows_reports" \
  '{cert_ts:$cert_ts,label_glob:$label,research_run_count:($run|tonumber),research_reduce_count:($reduce|tonumber),research_replay_count:($replay|tonumber),evidence_count:($evidence|tonumber),artifacts_count:($artifacts|tonumber),reports_count:($reports|tonumber)}' \
  > "$root/tmp/research-sql-cert-summary.json"

marker_ts="$cert_ts"
if [[ -z "$marker_ts" ]]; then
  marker_ts="$(date -u +%FT%TZ)"
fi
jq -n --arg cert_id "$cert_id" --arg cert_ts "$marker_ts" '{cert_id:$cert_id,cert_ts:$cert_ts}' > "$root/tmp/research-sql-cert.ok"
echo "research:sql-cert: OK"
