#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
mkdir -p "$root/tmp"
log="$root/tmp/cleanup-audit.log"

orphans="$(pgrep -x -a firecracker | rg -v 'defunct|rg -v' || true)"
stale_socks="$(find "$root/runs" -type s -name 'firecracker.sock' -print)"
stale_vsock="$(find "$root/runs" -type s -name 'vsock*.sock' -print)"
stale_fifos="$(find "$root/runs" -type p \( -name 'fc.log.fifo' -o -name 'fc.metrics.fifo' -o -name '*.fifo' \) -print)"
tap_leftovers="$(ip -o link show | rg 'virmux-tap' || true)"

{
  echo "ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  echo "orphans=${orphans:-none}"
  echo "stale_socks=${stale_socks:-none}"
  echo "stale_vsock=${stale_vsock:-none}"
  echo "stale_fifos=${stale_fifos:-none}"
  echo "tap_leftovers=${tap_leftovers:-none}"
} > "$log"

if [[ -n "$orphans" ]]; then
  echo "cleanup:audit: orphan firecracker process detected" >&2
  cat "$log" >&2
  exit 1
fi
if [[ -n "$stale_socks" ]]; then
  echo "cleanup:audit: stale firecracker sockets detected" >&2
  cat "$log" >&2
  exit 1
fi
if [[ -n "$stale_vsock" ]]; then
  echo "cleanup:audit: stale vsock sockets detected" >&2
  cat "$log" >&2
  exit 1
fi
if [[ -n "$stale_fifos" ]]; then
  echo "cleanup:audit: stale fifo files detected" >&2
  cat "$log" >&2
  exit 1
fi
if [[ -n "$tap_leftovers" ]]; then
  echo "cleanup:audit: leftover virmux tap devices detected" >&2
  cat "$log" >&2
  exit 1
fi

echo "cleanup:audit: OK"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/cleanup-audit.ok"
