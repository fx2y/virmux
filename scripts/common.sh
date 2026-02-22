#!/usr/bin/env bash
set -euo pipefail

repo_root() {
  if command -v git >/dev/null 2>&1; then
    git rev-parse --show-toplevel 2>/dev/null || pwd
    return
  fi
  pwd
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing required command: $cmd" >&2
    exit 1
  fi
}

calc_image_sha() {
  local root
  root="$(repo_root)"
  local manifest="$root/vm/image-src/manifest.json"
  local script="$root/scripts/image_build.sh"
  local inner="$root/scripts/image_build_inner.sh"
  local source_pins
  source_pins="$(
    jq -r '[.kernel_sha256,.rootfs_squashfs_sha256,.firecracker_tgz_sha256] | @tsv' "$manifest"
  )"
  local hasher_input
  local agentd_hashes=""
  if [[ -d "$root/cmd/virmux-agentd" || -d "$root/internal/agentd" ]]; then
    agentd_hashes="$(find "$root/cmd/virmux-agentd" "$root/internal/agentd" "$root/internal/transport" -type f -name '*.go' 2>/dev/null | sort | xargs -r sha256sum | awk '{print $1}' | tr '\n' ' ')"
  fi
  hasher_input="$(sha256sum "$manifest" "$script" "$inner" "$root/go.mod" "$root/go.sum" | awk '{print $1}' | tr '\n' ' ') $source_pins $agentd_hashes"
  printf '%s' "$hasher_input" | sha256sum | awk '{print $1}'
}

image_dir() {
  local root
  root="$(repo_root)"
  echo "$root/.cache/ghostfleet/images/$(calc_image_sha)"
}

read_image_sha_lock() {
  local root
  root="$(repo_root)"
  if [[ ! -f "$root/vm/images.lock" ]]; then
    echo "vm/images.lock missing; run: mise run image:stamp" >&2
    exit 1
  fi
  tr -d '[:space:]' < "$root/vm/images.lock"
}

firecracker_from_lock_or_path() {
  local root
  root="$(repo_root)"
  if [[ -f "$root/vm/images.lock" ]]; then
    local sha
    sha="$(read_image_sha_lock)"
    local bin="$root/.cache/ghostfleet/images/$sha/firecracker"
    if [[ -x "$bin" ]]; then
      echo "$bin"
      return 0
    fi
  fi
  if command -v firecracker >/dev/null 2>&1; then
    command -v firecracker
    return 0
  fi
  echo ""
}
