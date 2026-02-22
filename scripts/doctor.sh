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
pass "firecracker binary found: $fc_path"

sock_dir="$root/tmp/apisock"
mkdir -p "$sock_dir"
probe="$sock_dir/doctor.sock"
: > "$probe"
rm -f "$probe"
pass "api socket dir writable: $sock_dir"

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
