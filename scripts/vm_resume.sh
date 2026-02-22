#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"

go run ./cmd/virmux vm-resume \
  --images-lock "$root/vm/images.lock" \
  --runs-dir "$root/runs" \
  --db "$root/runs/virmux.sqlite" \
  "$@"

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/runs/.last-resume"
