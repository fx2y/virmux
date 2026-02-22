#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"

shopt -s nullglob
files=("$root"/runs/*/trace.jsonl)
if [[ ${#files[@]} -eq 0 ]]; then
  echo "trace:validate: no trace files in runs/*/trace.jsonl" >&2
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
      has("payload") and (.payload|type=="object")
    ' >/dev/null || {
      echo "trace:validate: schema failure file=$f line=$line_no" >&2
      exit 1
    }
  done < "$f"
  echo "trace:validate: OK: $f"
done

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/runs/.trace-validate.ok"
