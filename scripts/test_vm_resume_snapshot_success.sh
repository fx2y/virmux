#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
agent="resumeok-$RANDOM-$$"
label_prefix="resume-snapshot-$agent"

"$root/scripts/doctor.sh"

go run ./cmd/virmux vm-zygote \
  --images-lock "$root/vm/images.lock" \
  --runs-dir "$root/runs" \
  --db "$root/runs/virmux.sqlite" \
  --agent "$agent" \
  --label "$label_prefix-zygote" >/dev/null

out="$(go run ./cmd/virmux vm-resume \
  --images-lock "$root/vm/images.lock" \
  --runs-dir "$root/runs" \
  --db "$root/runs/virmux.sqlite" \
  --agent "$agent" \
  --label "$label_prefix-resume")"

printf '%s' "$out" | rg -q '"status":"ok"'
run_id="$(printf '%s' "$out" | jq -r '.run_id')"
mode="$(sqlite3 "$root/runs/virmux.sqlite" "select json_extract(payload,'$.resume_mode') from events where run_id='$run_id' and kind='run.finished' order by id desc limit 1;")"
if [[ "$mode" != "snapshot_resume" ]]; then
  echo "expected snapshot_resume mode, got: $mode" >&2
  exit 1
fi

echo "vm resume snapshot success: OK"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/vm-resume-snapshot-success.ok"
