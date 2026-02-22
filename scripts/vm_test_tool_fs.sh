#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
root="$(repo_root)"
agent="c3fs"
"$root/scripts/doctor.sh"
mise run image:stamp >/dev/null
go run ./cmd/virmux vm-run --agent "$agent" --images-lock "$root/vm/images.lock" --runs-dir "$root/runs" --db "$root/runs/virmux.sqlite" --vsock-cid 3 --tool fs.write --tool-args-json '{"path":"/data/c3.txt","bytes":"hello"}' --timeout-sec 20 >/dev/null
out="$(go run ./cmd/virmux vm-run --agent "$agent" --images-lock "$root/vm/images.lock" --runs-dir "$root/runs" --db "$root/runs/virmux.sqlite" --vsock-cid 3 --tool fs.read --tool-args-json '{"path":"/data/c3.txt"}' --timeout-sec 20)"
run_id="$(printf '%s' "$out" | jq -r '.run_id')"
res_json="$(jq -r 'select(.event=="vm.tool.result") | .payload.result' "$root/runs/$run_id/trace.ndjson" | tail -n1)"
jq -e '.ok==true and .data.bytes=="hello"' >/dev/null <<<"$res_json"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/vm-test-tool-fs.ok"
