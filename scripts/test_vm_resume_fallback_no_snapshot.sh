#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
agent="nosnap-$RANDOM-$$"
label="resume-fallback-$agent"

"$root/scripts/doctor.sh"

out="$(go run ./cmd/virmux vm-resume \
  --images-lock "$root/vm/images.lock" \
  --runs-dir "$root/runs" \
  --db "$root/runs/virmux.sqlite" \
  --agent "$agent" \
  --label "$label")"

printf '%s' "$out" | rg -q '"status":"ok"'
run_id="$(printf '%s' "$out" | jq -r '.run_id')"
mode="$(sqlite3 "$root/runs/virmux.sqlite" "select json_extract(payload,'$.resume_mode') from events where run_id='$run_id' and kind='run.finished' order by id desc limit 1;")"
source_kind="$(sqlite3 "$root/runs/virmux.sqlite" "select json_extract(payload,'$.resume_source') from events where run_id='$run_id' and kind='run.finished' order by id desc limit 1;")"
if [[ "$mode" != "fallback_cold_boot" ]]; then
  echo "expected fallback_cold_boot mode, got: $mode" >&2
  exit 1
fi
if [[ -z "$source_kind" ]]; then
  echo "missing resume_source in run.finished payload" >&2
  exit 1
fi

echo "vm resume fallback without snapshot metadata: OK"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/vm-resume-fallback-nosnap.ok"
