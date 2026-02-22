#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

root="$(repo_root)"
out_dir="$(image_dir)"
agentd_host_bin="$root/tmp/virmux-agentd-linux-amd64"
lock_dir="$root/.cache/ghostfleet/images/.build.lock"
lock_wait_sec="${VIRMUX_IMAGE_BUILD_LOCK_WAIT_SEC:-900}"
build_timeout_sec="${VIRMUX_IMAGE_BUILD_TIMEOUT_SEC:-1200}"
mkdir -p "$(dirname "$out_dir")"
mkdir -p "$root/tmp"

acquire_build_lock() {
  local start now pid
  start="$(date +%s)"
  while ! mkdir "$lock_dir" 2>/dev/null; do
    if [[ -f "$lock_dir/pid" ]]; then
      pid="$(cat "$lock_dir/pid" 2>/dev/null || true)"
      if [[ -n "$pid" ]] && ! kill -0 "$pid" 2>/dev/null; then
        rm -rf "$lock_dir"
        continue
      fi
    fi
    now="$(date +%s)"
    if (( now - start >= lock_wait_sec )); then
      echo "image build lock timeout (${lock_wait_sec}s): $lock_dir" >&2
      exit 1
    fi
    sleep 2
  done
  printf '%s\n' "$$" > "$lock_dir/pid"
}

release_build_lock() {
  rm -rf "$lock_dir"
}

acquire_build_lock
trap release_build_lock EXIT

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$agentd_host_bin" ./cmd/virmux-agentd

if [[ -f "$out_dir/.complete" ]]; then
  echo "image cache hit: $out_dir"
  exit 0
fi

if command -v docker >/dev/null 2>&1; then
  if command -v curl >/dev/null 2>&1 && command -v jq >/dev/null 2>&1 && command -v unsquashfs >/dev/null 2>&1 && command -v mksquashfs >/dev/null 2>&1 && command -v truncate >/dev/null 2>&1; then
    VIRMUX_AGENTD_HOST_BIN="$agentd_host_bin" ./scripts/image_build_inner.sh
  else
    host_uid="$(id -u)"
    host_gid="$(id -g)"
    if ! timeout "$build_timeout_sec" docker run --rm \
      -v "$root:$root" \
      -w "$root" \
      -e "HOST_UID=$host_uid" \
      -e "HOST_GID=$host_gid" \
      ubuntu:24.04 \
      bash -euo pipefail -c '
        export DEBIAN_FRONTEND=noninteractive
        apt-get update >/dev/null
        apt-get install -y --no-install-recommends ca-certificates curl jq squashfs-tools e2fsprogs tar coreutils >/dev/null
        VIRMUX_AGENTD_HOST_BIN="'"$agentd_host_bin"'" ./scripts/image_build_inner.sh
        source ./scripts/common.sh
        chown -R "${HOST_UID}:${HOST_GID}" "$(image_dir)"
      '
    then
      status="$?"
      if [[ "$status" -eq 124 ]]; then
        echo "image build timed out after ${build_timeout_sec}s (docker path)" >&2
      fi
      exit "$status"
    fi
  fi
else
  VIRMUX_AGENTD_HOST_BIN="$agentd_host_bin" ./scripts/image_build_inner.sh
fi

sha="$(calc_image_sha)"
mkdir -p "$root/.cache/ghostfleet/images"
printf '%s\n' "$sha" > "$root/.cache/ghostfleet/images/.manifest-built"
