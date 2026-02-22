#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

require_cmd jq
require_cmd curl
require_cmd tar
require_cmd unsquashfs
require_cmd mkfs.ext4
require_cmd sha256sum

root="$(repo_root)"
manifest="$root/vm/image-src/manifest.json"
out_dir="$(image_dir)"

mkdir -p "$out_dir"
if [[ -f "$out_dir/.complete" ]]; then
  echo "image cache hit: $out_dir"
  exit 0
fi

workdir="$(mktemp -d "$root/tmp/image-build.XXXXXX")"
cleanup() {
  rm -rf "$workdir"
}
trap cleanup EXIT

kernel_url="$(jq -r '.kernel_url' "$manifest")"
rootfs_url="$(jq -r '.rootfs_squashfs_url' "$manifest")"
firecracker_url="$(jq -r '.firecracker_tgz_url' "$manifest")"
rootfs_size="$(jq -r '.rootfs_size // "1G"' "$manifest")"

curl -fsSL "$kernel_url" -o "$workdir/vmlinux"
curl -fsSL "$rootfs_url" -o "$workdir/rootfs.squashfs"
curl -fsSL "$firecracker_url" -o "$workdir/firecracker.tgz"

unsquashfs -d "$workdir/squashfs-root" "$workdir/rootfs.squashfs" >/dev/null
truncate -s "$rootfs_size" "$workdir/rootfs.ext4"
mkfs.ext4 -d "$workdir/squashfs-root" -F "$workdir/rootfs.ext4" >/dev/null

tar -xzf "$workdir/firecracker.tgz" -C "$workdir"
arch="$(uname -m)"
firecracker_bin="$(find "$workdir" -type f -name "firecracker-v*-${arch}" ! -name '*.debug' | head -n 1)"
if [[ -z "$firecracker_bin" ]]; then
  firecracker_bin="$(find "$workdir" -type f -name firecracker ! -name '*.debug' | head -n 1)"
fi
if [[ -z "$firecracker_bin" ]]; then
  echo "failed to locate firecracker binary in archive" >&2
  exit 1
fi

install -m 0755 "$firecracker_bin" "$out_dir/firecracker"
install -m 0644 "$workdir/vmlinux" "$out_dir/vmlinux"
install -m 0644 "$workdir/rootfs.ext4" "$out_dir/rootfs.ext4"

jq -n \
  --arg kernel_url "$kernel_url" \
  --arg rootfs_url "$rootfs_url" \
  --arg firecracker_url "$firecracker_url" \
  --arg image_sha "$(calc_image_sha)" \
  --arg kernel_sha "$(sha256sum "$out_dir/vmlinux" | awk '{print $1}')" \
  --arg rootfs_sha "$(sha256sum "$out_dir/rootfs.ext4" | awk '{print $1}')" \
  --arg firecracker_sha "$(sha256sum "$out_dir/firecracker" | awk '{print $1}')" \
  '{image_sha:$image_sha,sources:{kernel:$kernel_url,rootfs:$rootfs_url,firecracker:$firecracker_url},sha256:{kernel:$kernel_sha,rootfs:$rootfs_sha,firecracker:$firecracker_sha}}' \
  > "$out_dir/metadata.json"

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$out_dir/.complete"
echo "image built: $out_dir"
