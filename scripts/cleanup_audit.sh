#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
mkdir -p "$root/tmp"
log="$root/tmp/cleanup-audit.log"

orphans="$(pgrep -fa firecracker | rg -v 'defunct|rg -v' || true)"
stale_socks="$(fd -t f 'firecracker\.sock$' "$root/runs" || true)"
tap_leftovers="$(ip -o link show | rg 'virmux-tap' || true)"

{
  echo "ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  echo "orphans=${orphans:-none}"
  echo "stale_socks=${stale_socks:-none}"
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
if [[ -n "$tap_leftovers" ]]; then
  echo "cleanup:audit: leftover virmux tap devices detected" >&2
  cat "$log" >&2
  exit 1
fi

echo "cleanup:audit: OK"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/cleanup-audit.ok"
