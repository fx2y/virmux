#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

root="$(repo_root)"
lock_path="$root/vm/images.lock"
[[ -f "$lock_path" ]] || {
  echo "doctor-guard: missing vm/images.lock; run mise run image:stamp first" >&2
  exit 1
}

sha="$(tr -d '[:space:]' < "$lock_path")"
[[ -n "$sha" ]] || {
  echo "doctor-guard: vm/images.lock is empty" >&2
  exit 1
}

target="$root/.cache/ghostfleet/images/$sha/rootfs.ext4"
[[ -f "$target" ]] || {
  echo "doctor-guard: missing artifact under lock: $target" >&2
  exit 1
}

backup="$target.bak.doctor-test.$$"
tmp_out="$(mktemp)"
cleanup() {
  if [[ -f "$backup" ]]; then
    mv "$backup" "$target"
  fi
  rm -f "$tmp_out"
}
trap cleanup EXIT

mv "$target" "$backup"
if "$root/scripts/doctor.sh" >"$tmp_out" 2>&1; then
  echo "doctor-guard: expected doctor to fail when rootfs artifact is missing" >&2
  cat "$tmp_out" >&2
  exit 1
fi
if ! rg -q "lock-selected artifact missing: .*rootfs.ext4" "$tmp_out"; then
  echo "doctor-guard: failure reason mismatch (expected missing rootfs)" >&2
  cat "$tmp_out" >&2
  exit 1
fi

mkdir -p "$root/tmp"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/doctor-missing-artifact.ok"
echo "doctor-guard: PASS"
