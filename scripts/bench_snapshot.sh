#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
iterations="${1:-5}"

for _ in $(seq 1 "$iterations"); do
  ./scripts/vm_resume.sh
  sleep 0.2
done

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/runs/.last-bench-snapshot"
