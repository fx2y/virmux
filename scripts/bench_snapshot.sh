#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
iterations="${1:-5}"
budget_p95_ms="${VIRMUX_BENCH_P95_MAX_MS:-6000}"
budget_p50_ms="${VIRMUX_BENCH_P50_MAX_MS:-3500}"
summary="$root/runs/bench-snapshot-summary.json"
label="bench-snapshot-$(date -u +%Y%m%dT%H%M%SZ)-$$"

for _ in $(seq 1 "$iterations"); do
  ./scripts/vm_resume.sh --label "$label"
  sleep 0.2
done

stats="$(sqlite3 -json "$root/runs/virmux.sqlite" "
WITH finish AS (
  SELECT
    run_id,
    json_extract(payload,'$.resume_mode') AS resume_mode
  FROM events
  WHERE kind='run.finished'
),
scoped AS (
  SELECT r.id AS run_id, r.resume_ms, f.resume_mode
  FROM runs r
  LEFT JOIN finish f ON f.run_id = r.id
  WHERE r.task='vm:resume' AND r.label='$label' AND r.status='ok'
),
ranked AS (
  SELECT
    CAST(resume_ms AS INTEGER) AS resume_ms,
    row_number() OVER (ORDER BY CAST(resume_ms AS INTEGER)) AS rn,
    count(*) OVER () AS cnt
  FROM scoped
  WHERE resume_mode='snapshot_resume'
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
  (SELECT COUNT(*) FROM scoped) AS total_samples,
  (SELECT COUNT(*) FROM scoped WHERE resume_mode='snapshot_resume') AS snapshot_resume_count,
  (SELECT COUNT(*) FROM scoped WHERE resume_mode='fallback_cold_boot') AS fallback_count,
  samples AS slo_samples,
  p50_ms,
  p95_ms,
  '$label' AS label,
  $iterations AS iterations,
  $budget_p50_ms AS budget_p50_ms,
  $budget_p95_ms AS budget_p95_ms,
  datetime('now') AS measured_at
FROM agg;")"

slo_samples="$(printf '%s' "$stats" | jq -r '.[0].slo_samples')"
total_samples="$(printf '%s' "$stats" | jq -r '.[0].total_samples')"
snapshot_resume_count="$(printf '%s' "$stats" | jq -r '.[0].snapshot_resume_count')"
fallback_count="$(printf '%s' "$stats" | jq -r '.[0].fallback_count')"
p50_ms="$(printf '%s' "$stats" | jq -r '.[0].p50_ms')"
p95_ms="$(printf '%s' "$stats" | jq -r '.[0].p95_ms')"
if [[ "$total_samples" -ne "$iterations" ]]; then
  echo "bench:snapshot: total_samples=$total_samples does not match iterations=$iterations" >&2
  exit 1
fi
if [[ "$snapshot_resume_count" -lt 1 ]]; then
  echo "bench:snapshot: no snapshot_resume samples found for label=$label" >&2
  exit 1
fi
if [[ "$snapshot_resume_count" -ne "$iterations" ]]; then
  echo "bench:snapshot: expected snapshot_resume_count=$iterations, got $snapshot_resume_count (label=$label)" >&2
  exit 1
fi
if [[ "$fallback_count" -ne 0 ]]; then
  echo "bench:snapshot: fallback_count must be 0 for perf cert, got $fallback_count (label=$label)" >&2
  exit 1
fi
if [[ "$slo_samples" -lt 1 ]]; then
  echo "bench:snapshot: no snapshot_resume SLO samples found for label=$label" >&2
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
