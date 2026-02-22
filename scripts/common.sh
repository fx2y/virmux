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
  local source_pins
  source_pins="$(
    jq -c '{kernel_sha256,rootfs_squashfs_sha256,firecracker_tgz_sha256}' "$manifest"
  )"

  {
    printf 'pins\0%s\0' "$source_pins"

    while IFS= read -r rel; do
      local path="$root/$rel"
      local sum
      sum="$(sha256sum "$path" | awk '{print $1}')"
      printf 'file\0%s\0%s\0' "$rel" "$sum"
    done <<'EOF'
vm/image-src/manifest.json
scripts/image_build.sh
scripts/image_build_inner.sh
go.mod
go.sum
EOF

    if [[ -d "$root/cmd/virmux-agentd" || -d "$root/internal/agentd" ]]; then
      find "$root/cmd/virmux-agentd" "$root/internal/agentd" "$root/internal/transport" \
        -type f -name '*.go' -print0 2>/dev/null \
        | sort -z \
        | while IFS= read -r -d '' path; do
          local rel="${path#$root/}"
          local sum
          sum="$(sha256sum "$path" | awk '{print $1}')"
          printf 'agentd\0%s\0%s\0' "$rel" "$sum"
        done
    fi
  } | sha256sum | awk '{print $1}'
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
