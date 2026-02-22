#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
iterations="${1:-5}"
budget_p95_ms="${VIRMUX_BENCH_P95_MAX_MS:-6000}"
budget_p50_ms="${VIRMUX_BENCH_P50_MAX_MS:-3500}"
summary="$root/runs/bench-snapshot-summary.json"

for _ in $(seq 1 "$iterations"); do
  ./scripts/vm_resume.sh
  sleep 0.2
done

stats="$(sqlite3 -json "$root/runs/virmux.sqlite" "
WITH ranked AS (
  SELECT
    CAST(resume_ms AS INTEGER) AS resume_ms,
    row_number() OVER (ORDER BY CAST(resume_ms AS INTEGER)) AS rn,
    count(*) OVER () AS cnt
  FROM runs
  WHERE task='vm:resume' AND status='ok'
  ORDER BY CAST(resume_ms AS INTEGER)
),
agg AS (
  SELECT
    cnt AS samples,
    COALESCE(MAX(CASE WHEN rn >= ((cnt * 50 + 99) / 100) THEN resume_ms END), 0) AS p50_ms,
    COALESCE(MAX(CASE WHEN rn >= ((cnt * 95 + 99) / 100) THEN resume_ms END), 0) AS p95_ms
  FROM ranked
)
SELECT
  samples,
  p50_ms,
  p95_ms,
  $iterations AS iterations,
  $budget_p50_ms AS budget_p50_ms,
  $budget_p95_ms AS budget_p95_ms,
  datetime('now') AS measured_at
FROM agg;")"

samples="$(printf '%s' "$stats" | jq -r '.[0].samples')"
p50_ms="$(printf '%s' "$stats" | jq -r '.[0].p50_ms')"
p95_ms="$(printf '%s' "$stats" | jq -r '.[0].p95_ms')"
if [[ "$samples" -lt 1 ]]; then
  echo "bench:snapshot: no vm:resume samples found" >&2
  exit 1
fi
if [[ "$p50_ms" -gt "$budget_p50_ms" ]]; then
  echo "bench:snapshot: p50 resume_ms=$p50_ms exceeds budget=$budget_p50_ms" >&2
  exit 1
fi
if [[ "$p95_ms" -gt "$budget_p95_ms" ]]; then
  echo "bench:snapshot: p95 resume_ms=$p95_ms exceeds budget=$budget_p95_ms" >&2
  exit 1
fi

printf '%s\n' "$stats" | jq '.[0] + {"host":"ubuntu24.04-baremetal","note":"baseline window Feb-2026"}' > "$summary"

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/runs/.last-bench-snapshot"
