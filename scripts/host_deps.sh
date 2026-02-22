#!/usr/bin/env bash
set -euo pipefail

pkgs=(
  qemu-system-x86
  qemu-utils
  iptables
  bridge-utils
  jq
  sqlite3
  squashfs-tools
  e2fsprogs
  curl
  ca-certificates
)

if [[ "${EUID}" -ne 0 ]]; then
  if command -v sudo >/dev/null 2>&1; then
    exec sudo "$0" "$@"
  fi
  echo "host:deps requires root (sudo unavailable)" >&2
  exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends "${pkgs[@]}"
repo_root="$(git rev-parse --show-toplevel)"
mkdir -p "$repo_root/tmp"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$repo_root/tmp/host-deps.ok"
