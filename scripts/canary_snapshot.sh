#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

db="$root/runs/virmux.sqlite"
out_dir="$root/dsets"
lookback_hours=24
limit=200
date_utc="$(date -u +%Y%m%d)"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) db="$2"; shift 2 ;;
    --out-dir) out_dir="$2"; shift 2 ;;
    --lookback-hours) lookback_hours="$2"; shift 2 ;;
    --limit) limit="$2"; shift 2 ;;
    --date) date_utc="$2"; shift 2 ;;
    *) echo "usage: scripts/canary_snapshot.sh [--db <path>] [--out-dir <dir>] [--lookback-hours <n>] [--limit <n>] [--date YYYYMMDD]" >&2; exit 1 ;;
  esac
done

for cmd in sqlite3 jq sha256sum; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "canary:snapshot: missing $cmd" >&2; exit 1; }
done
[[ -f "$db" ]] || { echo "canary:snapshot: missing db $db" >&2; exit 1; }
[[ "$lookback_hours" =~ ^[0-9]+$ ]] || { echo "canary:snapshot: lookback-hours must be integer" >&2; exit 1; }
[[ "$limit" =~ ^[0-9]+$ ]] || { echo "canary:snapshot: limit must be integer" >&2; exit 1; }
[[ "$date_utc" =~ ^[0-9]{8}$ ]] || { echo "canary:snapshot: date must be YYYYMMDD" >&2; exit 1; }

mkdir -p "$out_dir"
out_file="$out_dir/prod_${date_utc}.jsonl"
manifest_file="$out_dir/prod_${date_utc}.manifest.json"
if [[ -e "$out_file" || -e "$manifest_file" ]]; then
  echo "canary:snapshot: target already exists for date=$date_utc; use new date" >&2
  exit 1
fi

tmp_json="$(mktemp)"
trap 'rm -f "$tmp_json"' EXIT

sqlite3 -json "$db" "
WITH recent AS (
  SELECT
    r.id,
    r.task,
    r.label,
    r.status,
    r.trace_path,
    r.started_at,
    COALESCE(r.cost_est,0) AS cost_est,
    COALESCE((SELECT COUNT(*) FROM tool_calls tc WHERE tc.run_id=r.id),0) AS tool_calls,
    COALESCE((SELECT SUM(bytes) FROM artifacts a WHERE a.run_id=r.id),0) AS artifact_bytes
  FROM runs r
  WHERE datetime(r.started_at) >= datetime('now', '-${lookback_hours} hours')
  ORDER BY datetime(r.started_at) DESC, r.id DESC
  LIMIT ${limit}
)
SELECT * FROM recent ORDER BY id ASC;
" > "$tmp_json"

jq -c '.[] | {
  id: .id,
  input: {
    run_id: .id,
    task: .task,
    label: .label,
    status: .status,
    cost_est: .cost_est
  },
  context_refs: [(.trace_path // ("runs/" + .id + "/trace.ndjson"))],
  expected_properties: {
    status: "ok",
    max_tool_calls: (.tool_calls|tonumber)
  },
  tags: ([
    (if (.tool_calls|tonumber) >= 3 then "toolheavy" else empty end),
    (if (.artifact_bytes|tonumber) >= 4096 then "longctx" else empty end),
    (if .status != "ok" then "messy" else empty end)
  ] + [(.task|tostring)])
}' "$tmp_json" > "$out_file"

item_count="$(wc -l < "$out_file" | tr -d ' ')"
dset_sha="$(sha256sum "$out_file" | awk '{print $1}')"
rel_out="${out_file#$root/}"
rel_db="${db#$root/}"

toolheavy_count="$(jq -s '[.[]|select(.tags|index("toolheavy"))]|length' "$out_file")"
longctx_count="$(jq -s '[.[]|select(.tags|index("longctx"))]|length' "$out_file")"
messy_count="$(jq -s '[.[]|select(.tags|index("messy"))]|length' "$out_file")"

jq -n -S \
  --arg dset_path "$rel_out" \
  --arg dset_sha256 "$dset_sha" \
  --arg source_db "$rel_db" \
  --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --argjson lookback_hours "$lookback_hours" \
  --argjson limit "$limit" \
  --argjson items "$item_count" \
  --argjson toolheavy "$toolheavy_count" \
  --argjson longctx "$longctx_count" \
  --argjson messy "$messy_count" \
  '{schema:"dset.prod.v1",dset_path:$dset_path,dset_sha256:$dset_sha256,items:$items,source_db:$source_db,lookback_hours:$lookback_hours,limit:$limit,generated_at:$generated_at,tag_counts:{toolheavy:$toolheavy,longctx:$longctx,messy:$messy}}' \
  > "$manifest_file"

"$script_dir/dset_lint.sh" >/dev/null

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/canary-snapshot.ok"
echo "canary:snapshot: OK dset=$rel_out items=$item_count sha=$dset_sha"
