#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

root="$(repo_root)"
mkdir -p "$root/tmp" "$root/runs" "$root/.cache/ghostfleet/images"

fail() {
  echo "doctor: FAIL: $1" >&2
  exit 1
}

pass() {
  echo "doctor: OK: $1"
}

if ! rg -q '(vmx|svm)' /proc/cpuinfo; then
  fail "CPU virtualization flags missing (need vmx or svm)"
fi
pass "CPU virtualization flags present"

if ! lsmod | rg -q '^kvm' && [[ ! -d /sys/module/kvm ]]; then
  fail "kvm module is not loaded"
fi
pass "kvm module loaded"

if [[ ! -r /dev/kvm || ! -w /dev/kvm ]]; then
  fail "/dev/kvm is not readable+writable for current user"
fi
pass "/dev/kvm is readable+writable"

fc_path="$(firecracker_from_lock_or_path || true)"
if [[ -z "$fc_path" ]]; then
  fail "firecracker binary not found (PATH or image cache from vm/images.lock)"
fi
if [[ ! -x "$fc_path" ]]; then
  fail "firecracker binary is not executable: $fc_path"
fi
pass "firecracker binary found: $fc_path"

lock_path="$root/vm/images.lock"
if [[ -f "$lock_path" ]]; then
  sha="$(tr -d '[:space:]' < "$lock_path")"
  if [[ -z "$sha" ]]; then
    fail "vm/images.lock is empty"
  fi
  image_dir="$root/.cache/ghostfleet/images/$sha"
  for artifact in firecracker vmlinux rootfs.ext4; do
    target="$image_dir/$artifact"
    if [[ ! -f "$target" ]]; then
      fail "lock-selected artifact missing: $target"
    fi
  done
  if [[ ! -x "$image_dir/firecracker" ]]; then
    fail "lock-selected firecracker is not executable: $image_dir/firecracker"
  fi
  pass "lock-selected artifacts exist: $image_dir/{firecracker,vmlinux,rootfs.ext4}"
else
  pass "vm/images.lock absent; artifact-set check skipped (bootstrap mode)"
fi

if ! command -v python3 >/dev/null 2>&1; then
  fail "python3 is required for unix socket probe"
fi
sock_dir="${VIRMUX_APISOCK_DIR:-$root/tmp/apisock}"
mkdir -p "$sock_dir"
if ! VIRMUX_DOCTOR_PROBE_DIR="$sock_dir" python3 - <<'PY'
import os
import socket
import tempfile

sock_dir = os.environ["VIRMUX_DOCTOR_PROBE_DIR"]
fd, path = tempfile.mkstemp(prefix="doctor.", suffix=".sock", dir=sock_dir)
os.close(fd)
os.unlink(path)
s = socket.socket(socket.AF_UNIX)
try:
    s.bind(path)
finally:
    s.close()
if os.path.exists(path):
    os.unlink(path)
PY
then
  fail "unix socket bind probe failed for dir: $sock_dir"
fi
pass "api socket bind/unlink probe passed: $sock_dir"

nofile="$(ulimit -n)"
min_nofile="${VIRMUX_MIN_NOFILE:-1024}"
if [[ "$nofile" -lt "$min_nofile" ]]; then
  fail "ulimit -n too low ($nofile < $min_nofile)"
fi
pass "ulimit -n=$nofile"

for d in "$root/runs" "$root/tmp" "$root/.cache/ghostfleet/images"; do
  [[ -d "$d" ]] || fail "missing required directory: $d"
done
pass "required directories exist"

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/doctor.ok"
echo "doctor: PASS"
