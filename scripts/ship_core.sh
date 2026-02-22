#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
cd "$root"

mkdir -p "$root/tmp" "$root/runs"
cert_ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
cert_tag="qa-cert-$(date -u +%Y%m%dT%H%M%SZ)-$$"

mise run ci:fast
./scripts/doctor.sh
./scripts/test_doctor_missing_artifact.sh
./scripts/test_doctor_socket_probe.sh
./scripts/test_image_checksum_mismatch.sh
./scripts/vm_boot_contract.sh
./scripts/vm_vsock_chaos.sh
./scripts/vm_smoke.sh --label "$cert_tag-smoke"
./scripts/test_vm_agent_persistence.sh
./scripts/test_vm_resume_fallback_no_snapshot.sh
./scripts/test_vm_resume_snapshot_success.sh
./scripts/vm_zygote.sh --label "$cert_tag-zygote"
./scripts/vm_resume.sh --label "$cert_tag-resume"
./scripts/bench_snapshot.sh
./scripts/trace_validate.sh
./scripts/db_check.sh
VIRMUX_CERT_LABEL_GLOB="$cert_tag-%" ./scripts/sql_cert_contract.sh
./scripts/cleanup_audit.sh

for task in vm:smoke vm:zygote vm:resume; do
  fresh_count="$(sqlite3 "$root/runs/virmux.sqlite" "
SELECT COUNT(*)
FROM runs
WHERE task='$task'
  AND started_at >= '$cert_ts'
  AND label LIKE '$cert_tag-%';")"
  if [[ "$fresh_count" -lt 1 ]]; then
    echo "ship:core: expected fresh run for task=$task at/after cert_ts=$cert_ts label_prefix=$cert_tag-, got $fresh_count" >&2
    exit 1
  fi
done

jq -n \
  --arg cert_ts "$cert_ts" \
  --arg cert_tag "$cert_tag" \
  --arg finished_at "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  '{cert_ts:$cert_ts,cert_tag:$cert_tag,finished_at:$finished_at,fresh_tasks:["vm:smoke","vm:zygote","vm:resume"]}' \
  > "$root/tmp/ship-core-summary.json"

echo "ship:core: OK cert_tag=$cert_tag"
