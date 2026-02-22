#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
"$root/scripts/doctor.sh"

jobs="${MISE_JOBS:-$(nproc)}"
seq 1 "$jobs" | xargs -I{} -P "$jobs" bash -euo pipefail -c 'VIRMUX_SKIP_DOCTOR=1 ./scripts/vm_smoke.sh --label parallel-{}'
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/runs/.last-smoke-parallel"
