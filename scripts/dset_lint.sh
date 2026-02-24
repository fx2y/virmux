#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "dset:lint: missing required command: $1" >&2
    exit 1
  fi
}

require_cmd jq

files=()
for pat in dsets/smoke/*.jsonl dsets/core/*.jsonl dsets/torture/*.jsonl dsets/prod_*.jsonl; do
  for f in $pat; do
    if [[ -f "$f" ]]; then
      files+=("$f")
    fi
  done
done

if [[ ${#files[@]} -eq 0 ]]; then
  echo "dset:lint: no dataset files found under dsets/{smoke,core,torture} or dsets/prod_*.jsonl" >&2
  exit 1
fi

schema='type=="object" and (keys|sort)==["context_refs","expected_properties","id","input","tags"] and (.id|type=="string" and length>0) and (.input|type=="object") and (.context_refs|type=="array") and (.expected_properties|type=="object") and (.tags|type=="array")'

for f in "${files[@]}"; do
  jq -e -c "$schema" "$f" >/dev/null || {
    echo "dset:lint: schema check failed for $f" >&2
    exit 1
  }
  dups="$(jq -r '.id' "$f" | sort | uniq -d)"
  if [[ -n "$dups" ]]; then
    echo "dset:lint: duplicate ids in $f: $dups" >&2
    exit 1
  fi
  if [[ "$(wc -l < "$f" | tr -d ' ')" -eq 0 ]]; then
    echo "dset:lint: empty dataset file $f" >&2
    exit 1
  fi
done

# Append/new-version semantics: changed tracked JSONL dataset files are denied.
# New files (A/??) are allowed.
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  bad="$(git status --porcelain -- dsets | awk '$2 ~ /\.jsonl$/ && $1 !~ /^(A|\?\?)/ {print $0}')"
  if [[ -n "$bad" ]]; then
    echo "dset:lint: in-place dataset mutation denied; create new versioned file instead" >&2
    echo "$bad" >&2
    exit 1
  fi
fi

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/dset-lint.ok"
echo "dset:lint: OK files=${#files[@]}"
