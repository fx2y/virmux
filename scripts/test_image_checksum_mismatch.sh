#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
manifest="$root/vm/image-src/manifest.json"
backup="$(mktemp "$root/tmp/manifest.backup.XXXXXX")"
trap 'mv "$backup" "$manifest"' EXIT

mkdir -p "$root/tmp"
cp "$manifest" "$backup"

tmp_manifest="$(mktemp "$root/tmp/manifest.bad.XXXXXX")"
jq '.kernel_sha256 = "deadbeef"' "$backup" > "$tmp_manifest"
mv "$tmp_manifest" "$manifest"

if ./scripts/image_build_inner.sh >/tmp/virmux-image-checksum-test.log 2>&1; then
  echo "expected image build to fail on checksum mismatch" >&2
  exit 1
fi

if ! rg -q "checksum mismatch" /tmp/virmux-image-checksum-test.log; then
  echo "expected checksum mismatch error output" >&2
  cat /tmp/virmux-image-checksum-test.log >&2
  exit 1
fi

echo "image checksum mismatch guard: OK"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/image-checksum-mismatch.ok"
