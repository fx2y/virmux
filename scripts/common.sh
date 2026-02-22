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
  local hasher_input
  hasher_input="$(sha256sum "$manifest" "$script" "$inner" | awk '{print $1}' | tr '\n' ' ')"
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
