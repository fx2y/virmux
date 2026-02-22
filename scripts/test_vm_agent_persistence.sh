#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"

"$root/scripts/doctor.sh"

go run ./cmd/virmux vm-run \
  --images-lock "$root/vm/images.lock" \
  --runs-dir "$root/runs" \
  --db "$root/runs/virmux.sqlite" \
  --agent A \
  --label persist-1 \
  --cmd "echo hi >/dev/virmux-data/hi.txt; sync" >/dev/null

run2_json="$(go run ./cmd/virmux vm-run \
  --images-lock "$root/vm/images.lock" \
  --runs-dir "$root/runs" \
  --db "$root/runs/virmux.sqlite" \
  --agent A \
  --label persist-2 \
  --cmd "cat /dev/virmux-data/hi.txt")"
printf '%s' "$run2_json" | rg -q '"status":"ok"'
run2_dir="$(printf '%s' "$run2_json" | jq -r '.run_dir')"
rg -q '(^|[[:space:]])hi([[:space:]]|$)' "$run2_dir/serial.log"

go run ./cmd/virmux vm-run \
  --images-lock "$root/vm/images.lock" \
  --runs-dir "$root/runs" \
  --db "$root/runs/virmux.sqlite" \
  --agent B \
  --label persist-iso \
  --cmd "test ! -f /dev/virmux-data/hi.txt" >/dev/null

sqlite3 "$root/runs/virmux.sqlite" "select count(*) from runs where agent_id in ('A','B');" | rg -q '^[1-9]'
[[ -f "$root/agents/A.json" ]]
[[ -f "$root/volumes/A.ext4" ]]

echo "agent persistence: OK"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/vm-agent-persistence.ok"
