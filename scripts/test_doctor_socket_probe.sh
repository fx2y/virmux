#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

root="$(repo_root)"
probe_dir="$(mktemp -d "$root/tmp/doctor-sock.XXXXXX")"
tmp_out="$(mktemp)"
cleanup() {
  chmod 0755 "$probe_dir" 2>/dev/null || true
  rm -rf "$probe_dir"
  rm -f "$tmp_out"
}
trap cleanup EXIT

chmod 0555 "$probe_dir"
if VIRMUX_APISOCK_DIR="$probe_dir" "$root/scripts/doctor.sh" >"$tmp_out" 2>&1; then
  echo "doctor-sock-guard: expected doctor to fail for non-writable socket dir" >&2
  cat "$tmp_out" >&2
  exit 1
fi
if ! rg -q "unix socket bind probe failed for dir" "$tmp_out"; then
  echo "doctor-sock-guard: failure reason mismatch" >&2
  cat "$tmp_out" >&2
  exit 1
fi

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/doctor-socket-probe.ok"
echo "doctor-sock-guard: PASS"
