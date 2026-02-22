#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
cd "$root"

sha1="$(calc_image_sha)"
sha2="$(calc_image_sha)"
if [[ "$sha1" != "$sha2" ]]; then
  echo "calc_image_sha instability detected: $sha1 != $sha2" >&2
  exit 1
fi

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/image-sha-stability.ok"
echo "calc_image_sha stability: OK sha=$sha1"
