#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

root="$(repo_root)"
sha="$(calc_image_sha)"
dir="$root/.cache/ghostfleet/images/$sha"

if [[ ! -f "$dir/.complete" ]]; then
  echo "image not built for sha=$sha; run: mise run image:build" >&2
  exit 1
fi

printf '%s\n' "$sha" > "$root/vm/images.lock"
echo "vm/images.lock -> $sha"
