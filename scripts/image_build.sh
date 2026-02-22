#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
require_cmd flock

root="$(repo_root)"
out_dir="$(image_dir)"
agentd_host_bin="$root/tmp/virmux-agentd-linux-amd64"
lock_dir_legacy="$root/.cache/ghostfleet/images/.build.lock"
lock_file="$root/.cache/ghostfleet/images/.build.lock.flock"
lock_meta="$root/.cache/ghostfleet/images/.build.lock.meta"
lock_wait_sec="${VIRMUX_IMAGE_BUILD_LOCK_WAIT_SEC:-900}"
build_timeout_sec="${VIRMUX_IMAGE_BUILD_TIMEOUT_SEC:-1200}"
hold_sec="${VIRMUX_IMAGE_BUILD_HOLD_SEC:-0}"
lock_log_every_sec="${VIRMUX_IMAGE_BUILD_LOCK_LOG_EVERY_SEC:-10}"
mkdir -p "$(dirname "$out_dir")"
mkdir -p "$root/tmp"

wait_for_legacy_lock() {
  local start now pid mtime age last_log
  [[ -d "$lock_dir_legacy" ]] || return 0
  start="$(date +%s)"
  last_log=0
  while [[ -d "$lock_dir_legacy" ]]; do
    pid="$(cat "$lock_dir_legacy/pid" 2>/dev/null || true)"
    if [[ -n "$pid" ]] && ! kill -0 "$pid" 2>/dev/null; then
      rm -rf "$lock_dir_legacy"
      break
    fi
    if [[ -z "$pid" ]]; then
      mtime="$(stat -c '%Y' "$lock_dir_legacy" 2>/dev/null || date +%s)"
      now="$(date +%s)"
      age=$((now - mtime))
      if (( age > 300 )); then
        rm -rf "$lock_dir_legacy"
        break
      fi
    fi
    now="$(date +%s)"
    if (( now - start >= lock_wait_sec )); then
      echo "image build legacy lock timeout (${lock_wait_sec}s): $lock_dir_legacy pid=${pid:-unknown}" >&2
      exit 1
    fi
    if (( now - last_log >= lock_log_every_sec )); then
      echo "image build waiting for legacy lock: $lock_dir_legacy pid=${pid:-unknown}" >&2
      last_log="$now"
    fi
    sleep 1
  done
}

release_build_meta() {
  rm -f "$lock_meta"
}

run_under_build_lock() {
  local start now last_log holder status
  wait_for_legacy_lock
  start="$(date +%s)"
  last_log=0
  while true; do
    if flock -n -E 200 "$lock_file" -c "VIRMUX_IMAGE_BUILD_LOCKED=1 \"$0\""; then
      exit 0
    fi
    status="$?"
    if [[ "$status" -ne 200 ]]; then
      exit "$status"
    fi
    now="$(date +%s)"
    if (( now - start >= lock_wait_sec )); then
      holder="$(cat "$lock_meta" 2>/dev/null || true)"
      echo "image build lock timeout (${lock_wait_sec}s): $lock_file holder=${holder:-unknown}" >&2
      exit 1
    fi
    if (( now - last_log >= lock_log_every_sec )); then
      holder="$(cat "$lock_meta" 2>/dev/null || true)"
      echo "image build waiting for lock: $lock_file holder=${holder:-unknown}" >&2
      last_log="$now"
    fi
    sleep 1
  done
}

if [[ "${VIRMUX_IMAGE_BUILD_LOCKED:-0}" != "1" ]]; then
  run_under_build_lock
fi

trap release_build_meta EXIT
printf 'pid=%s started=%s\n' "$$" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$lock_meta"

if [[ "$hold_sec" =~ ^[0-9]+$ ]] && (( hold_sec > 0 )); then
  sleep "$hold_sec"
fi
if [[ "${VIRMUX_IMAGE_BUILD_SKIP_WORK:-0}" == "1" ]]; then
  exit 0
fi

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
