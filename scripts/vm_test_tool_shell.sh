#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
root="$(repo_root)"
"$root/scripts/doctor.sh"
mise run image:stamp >/dev/null
out="$(go run ./cmd/virmux vm-run --images-lock "$root/vm/images.lock" --runs-dir "$root/runs" --db "$root/runs/virmux.sqlite" --vsock-cid 3 --tool shell.exec --cmd 'echo hi' --timeout-sec 20)"
run_id="$(printf '%s' "$out" | jq -r '.run_id')"
trace="$root/runs/$run_id/trace.ndjson"
res_json="$(jq -r 'select(.event=="vm.tool.result") | .payload.result // empty' "$trace" | tail -n1)"
if [[ -z "$res_json" || "$res_json" == "null" ]]; then
  out_ref="$(jq -r 'select(.event=="vm.tool.result") | .payload.output_ref // empty' "$trace" | tail -n1)"
  [[ -n "$out_ref" ]] || { echo "missing vm.tool.result output_ref" >&2; exit 1; }
  res_json="$(cat "$root/runs/$run_id/$out_ref")"
fi
jq -e '.ok==true and .rc==0' >/dev/null <<<"$res_json"
rg -q 'hi' "$root/runs/$run_id/artifacts/1.out"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/vm-test-tool-shell.ok"
