#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
mkdir -p "$root/tmp" "$root/runs"

ok_artifact="$root/tmp/vm-boot-contract.json"
fail_artifact="$root/tmp/vm-boot-contract.fail.json"
rm -f "$ok_artifact" "$fail_artifact"

fail() {
  local reason="$1"
  local run_id="${2:-}"
  jq -n \
    --arg ts "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg reason "$reason" \
    --arg run_id "$run_id" \
    '{ts:$ts,status:"failed",reason:$reason,run_id:$run_id}' > "$fail_artifact"
  echo "vm:boot:contract: FAIL: $reason" >&2
  exit 1
}

label="boot-contract-$(date -u +%Y%m%dT%H%M%SZ)-$$"
"$root/scripts/vm_smoke.sh" --label "$label"

run_id="$(sqlite3 "$root/runs/virmux.sqlite" "
SELECT id FROM runs
WHERE task='vm:smoke' AND label='$label'
ORDER BY started_at DESC
LIMIT 1;")"

if [[ -z "$run_id" ]]; then
  fail "missing vm:smoke run row for label=$label"
fi

run_dir="$root/runs/$run_id"
[[ -f "$run_dir/fc.log" ]] || fail "missing firecracker log file" "$run_id"
[[ -f "$run_dir/fc.metrics.log" ]] || fail "missing firecracker metrics log file" "$run_id"
[[ -f "$run_dir/trace.ndjson" ]] || fail "missing primary trace.ndjson" "$run_id"

guest_ready_count="$(sqlite3 "$root/runs/virmux.sqlite" "
SELECT COUNT(*)
FROM events
WHERE run_id='$run_id' AND kind='vm.guest.ready';")"
if [[ "$guest_ready_count" -lt 1 ]]; then
  fail "missing vm.guest.ready boundary event" "$run_id"
fi

lost_logs="$(sqlite3 "$root/runs/virmux.sqlite" "
SELECT COALESCE(CAST(json_extract(payload,'$.lost_logs') AS INTEGER), -1)
FROM events
WHERE run_id='$run_id' AND kind='run.finished'
ORDER BY id DESC
LIMIT 1;")"
lost_metrics="$(sqlite3 "$root/runs/virmux.sqlite" "
SELECT COALESCE(CAST(json_extract(payload,'$.lost_metrics') AS INTEGER), -1)
FROM events
WHERE run_id='$run_id' AND kind='run.finished'
ORDER BY id DESC
LIMIT 1;")"

if [[ "$lost_logs" -lt 0 || "$lost_metrics" -lt 0 ]]; then
  fail "run.finished missing non-null lost_logs/lost_metrics" "$run_id"
fi
if [[ "$lost_logs" -gt 0 || "$lost_metrics" -gt 0 ]]; then
  fail "lost counter breach lost_logs=$lost_logs lost_metrics=$lost_metrics" "$run_id"
fi

jq -n \
  --arg ts "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --arg run_id "$run_id" \
  --arg label "$label" \
  --argjson lost_logs "$lost_logs" \
  --argjson lost_metrics "$lost_metrics" \
  --argjson guest_ready_events "$guest_ready_count" \
  '{ts:$ts,status:"ok",run_id:$run_id,label:$label,lost_logs:$lost_logs,lost_metrics:$lost_metrics,guest_ready_events:$guest_ready_events}' > "$ok_artifact"

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/vm-boot-contract.ok"
echo "vm:boot:contract: OK run_id=$run_id"
