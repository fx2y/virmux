#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"

mkdir -p "$root/runs"
stamp="$root/runs/.last-vm-net-probe"
tap="virmux-tap0"

cleanup() {
  ip link del "$tap" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if [[ "$(id -u)" -ne 0 ]]; then
  echo "vm:net:probe: skipped (needs root for tap setup)"
  printf '%s\n' "{\"status\":\"skipped\",\"reason\":\"not_root\",\"ts\":\"$(date -u +"%Y-%m-%dT%H:%M:%SZ")\"}" > "$stamp"
  exit 0
fi

"$root/scripts/doctor.sh"

ip link del "$tap" >/dev/null 2>&1 || true
ip tuntap add dev "$tap" mode tap
ip addr add 172.31.0.1/30 dev "$tap"
ip link set dev "$tap" up
echo "vm:net:probe: tap setup ok ($tap)"
printf '%s\n' "{\"status\":\"ok\",\"tap\":\"$tap\",\"ts\":\"$(date -u +"%Y-%m-%dT%H:%M:%SZ")\"}" > "$stamp"
