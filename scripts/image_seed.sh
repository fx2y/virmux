#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

root="$(repo_root)"
src_dir="${1:-${VIRMUX_IMAGE_SEED_FROM:-}}"
if [[ -z "$src_dir" ]]; then
  echo "usage: scripts/image_seed.sh <source-image-dir>" >&2
  exit 1
fi
if [[ ! -d "$src_dir" ]]; then
  echo "image:seed source directory not found: $src_dir" >&2
  exit 1
fi

dest_dir="${VIRMUX_IMAGE_SEED_DEST_DIR:-$(image_dir)}"
mkdir -p "$dest_dir"

for name in firecracker vmlinux rootfs.ext4; do
  src="$src_dir/$name"
  if [[ ! -f "$src" ]]; then
    echo "image:seed missing source artifact: $src" >&2
    exit 1
  fi
done
if [[ ! -x "$src_dir/firecracker" ]]; then
  echo "image:seed source firecracker is not executable: $src_dir/firecracker" >&2
  exit 1
fi

tmp_dir="$(mktemp -d "$root/tmp/image-seed.XXXXXX")"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

install -m 0755 "$src_dir/firecracker" "$tmp_dir/firecracker"
install -m 0644 "$src_dir/vmlinux" "$tmp_dir/vmlinux"
install -m 0644 "$src_dir/rootfs.ext4" "$tmp_dir/rootfs.ext4"
if [[ -f "$src_dir/metadata.json" ]]; then
  install -m 0644 "$src_dir/metadata.json" "$tmp_dir/metadata.json"
fi

firecracker_sha="$(sha256sum "$tmp_dir/firecracker" | awk '{print $1}')"
kernel_sha="$(sha256sum "$tmp_dir/vmlinux" | awk '{print $1}')"
rootfs_sha="$(sha256sum "$tmp_dir/rootfs.ext4" | awk '{print $1}')"
jq -n \
  --arg seeded_from "$src_dir" \
  --arg image_sha "$(calc_image_sha)" \
  --arg firecracker_sha "$firecracker_sha" \
  --arg kernel_sha "$kernel_sha" \
  --arg rootfs_sha "$rootfs_sha" \
  '{seeded_from:$seeded_from,image_sha:$image_sha,sha256:{firecracker:$firecracker_sha,kernel:$kernel_sha,rootfs:$rootfs_sha}}' > "$tmp_dir/seeded.json"

mv "$tmp_dir/firecracker" "$dest_dir/firecracker"
mv "$tmp_dir/vmlinux" "$dest_dir/vmlinux"
mv "$tmp_dir/rootfs.ext4" "$dest_dir/rootfs.ext4"
if [[ -f "$tmp_dir/metadata.json" ]]; then
  mv "$tmp_dir/metadata.json" "$dest_dir/metadata.json"
fi
mv "$tmp_dir/seeded.json" "$dest_dir/seeded.json"
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$dest_dir/.complete"

mkdir -p "$root/.cache/ghostfleet/images"
printf '%s\n' "$(calc_image_sha)" > "$root/.cache/ghostfleet/images/.manifest-built"

echo "image seeded: $dest_dir"
