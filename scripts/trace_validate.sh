#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"

shopt -s nullglob
files=("$root"/runs/*/trace.ndjson "$root"/runs/*/trace.jsonl)
if [[ ${#files[@]} -eq 0 ]]; then
  echo "trace:validate: no trace files in runs/*/{trace.ndjson,trace.jsonl}" >&2
  exit 1
fi

for f in "${files[@]}"; do
  line_no=0
  while IFS= read -r line || [[ -n "$line" ]]; do
    line_no=$((line_no + 1))
    printf '%s\n' "$line" | jq -e '
      type == "object" and
      has("ts") and (.ts|type=="string") and
      has("run_id") and (.run_id|type=="string") and
      has("task") and (.task|type=="string") and
      has("event") and (.event|type=="string") and
      has("payload") and (.payload|type=="object") and
      (
        ((has("seq")|not) and (has("type")|not)) or
        ((.seq|type=="number" and . > 0) and (.type|type=="string"))
      )
    ' >/dev/null || {
      echo "trace:validate: schema failure file=$f line=$line_no" >&2
      exit 1
    }
    printf '%s\n' "$line" | jq -e '
      if .event == "vm.tool.result" then
        if .type == "tool" then
          (.tool|type=="string" and length>0) and
          (.args_hash|type=="string" and startswith("sha256:")) and
          (.stdout_ref|type=="string") and
          (.stderr_ref|type=="string") and
          (.exit_code|type=="number") and
          (.dur_ms|type=="number") and
          (.bytes_in|type=="number") and
          (.bytes_out|type=="number")
        else
          true
        end
      else
        true
      end
    ' >/dev/null || {
      echo "trace:validate: tool receipt failure file=$f line=$line_no" >&2
      exit 1
    }
  done < "$f"
  echo "trace:validate: OK: $f"
done

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/runs/.trace-validate.ok"
