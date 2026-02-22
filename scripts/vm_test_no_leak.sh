#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
root="$(repo_root)"
"$root/scripts/doctor.sh"
mise run image:stamp >/dev/null
out="$(go run ./cmd/virmux vm-run --images-lock "$root/vm/images.lock" --runs-dir "$root/runs" --db "$root/runs/virmux.sqlite" --vsock-cid 3 --tool fs.write --tool-args-json '{"path":"/etc/deny.txt","bytes":"x"}' --timeout-sec 20 || true)"
run_id="$(printf '%s' "$out" | jq -r '.run_id // empty')"
[[ -n "$run_id" ]]
res_json="$(jq -r 'select(.event=="vm.tool.result") | .payload.result' "$root/runs/$run_id/trace.ndjson" | tail -n1)"
jq -e '.ok==false and .error.code=="DENIED"' >/dev/null <<<"$res_json"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/vm-test-no-leak.ok"
