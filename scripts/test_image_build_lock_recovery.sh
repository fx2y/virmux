#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
cd "$root"
mkdir -p "$root/tmp"

log="$root/tmp/image-build-lock-recovery.log"
lock_meta="$root/.cache/ghostfleet/images/.build.lock.meta"
rm -f "$log"

VIRMUX_IMAGE_BUILD_SKIP_WORK=1 \
VIRMUX_IMAGE_BUILD_HOLD_SEC=120 \
VIRMUX_IMAGE_BUILD_LOCK_WAIT_SEC=10 \
./scripts/image_build.sh >"$log" 2>&1 &
launcher_pid="$!"

ready=0
for _ in $(seq 1 50); do
  if [[ -f "$lock_meta" ]]; then
    ready=1
    break
  fi
  sleep 0.1
done
if [[ "$ready" -ne 1 ]]; then
  kill "$launcher_pid" 2>/dev/null || true
  wait "$launcher_pid" 2>/dev/null || true
  echo "lock recovery test failed: lock meta not created" >&2
  exit 1
fi

holder_pid="$(sed -n 's/^pid=\([0-9][0-9]*\).*/\1/p' "$lock_meta" | tail -n1)"
if [[ -z "$holder_pid" ]]; then
  kill "$launcher_pid" 2>/dev/null || true
  wait "$launcher_pid" 2>/dev/null || true
  echo "lock recovery test failed: lock holder pid missing in $lock_meta" >&2
  exit 1
fi

kill -9 "$holder_pid" 2>/dev/null || true
kill -9 "$launcher_pid" 2>/dev/null || true
wait "$launcher_pid" 2>/dev/null || true

VIRMUX_IMAGE_BUILD_SKIP_WORK=1 \
VIRMUX_IMAGE_BUILD_LOCK_WAIT_SEC=5 \
./scripts/image_build.sh >/dev/null

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/image-build-lock-recovery.ok"
echo "image build lock recovery: OK"
