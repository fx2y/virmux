#!/usr/bin/env bash
# scripts/vm_cleanup.sh - actually remove stale artifacts flagged by cleanup_audit
set -euo pipefail
source "$(dirname "$0")/common.sh"

echo "vm:cleanup: starting at $(date -u +%FT%TZ)"

# 1. Kill orphan firecracker processes
ORPHANS=$(pgrep -x firecracker || true)
if [[ -n "$ORPHANS" ]]; then
  echo "vm:cleanup: killing orphan firecracker processes: $ORPHANS"
  kill -9 $ORPHANS || true
fi

# 2. Remove stale sockets/fifos in runs/
find runs -name "*.sock" -type s -delete -print || true
find runs -name "*.fifo" -type p -delete -print || true

# 3. Cleanup tap interfaces (needs sudo, so we just echo what to do if found)
TAPS=$(ip link show | grep -o 'virmux-tap[0-9]\+' || true)
if [[ -n "$TAPS" ]]; then
  echo "vm:cleanup: found leftover tap interfaces: $TAPS"
  echo "vm:cleanup: run 'sudo ./scripts/vm_net_cleanup.sh' if they persist"
fi

echo "vm:cleanup: OK"
