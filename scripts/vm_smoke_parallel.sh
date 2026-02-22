#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"

jobs="${MISE_JOBS:-$(nproc)}"
seq 1 "$jobs" | xargs -I{} -P "$jobs" bash -euo pipefail -c './scripts/vm_smoke.sh --label parallel-{}'
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/runs/.last-smoke-parallel"
