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
kernel_expected_sha="$(jq -r '.kernel_sha256' "$manifest")"
rootfs_expected_sha="$(jq -r '.rootfs_squashfs_sha256' "$manifest")"
firecracker_expected_sha="$(jq -r '.firecracker_tgz_sha256' "$manifest")"
rootfs_size="$(jq -r '.rootfs_size // "1G"' "$manifest")"

for required in "$kernel_expected_sha" "$rootfs_expected_sha" "$firecracker_expected_sha"; do
  if [[ -z "$required" || "$required" == "null" ]]; then
    echo "manifest requires kernel_sha256/rootfs_squashfs_sha256/firecracker_tgz_sha256" >&2
    exit 1
  fi
done

curl -fsSL "$kernel_url" -o "$workdir/vmlinux"
curl -fsSL "$rootfs_url" -o "$workdir/rootfs.squashfs"
curl -fsSL "$firecracker_url" -o "$workdir/firecracker.tgz"

kernel_actual_sha="$(sha256sum "$workdir/vmlinux" | awk '{print $1}')"
rootfs_actual_sha="$(sha256sum "$workdir/rootfs.squashfs" | awk '{print $1}')"
firecracker_actual_sha="$(sha256sum "$workdir/firecracker.tgz" | awk '{print $1}')"
if [[ "$kernel_actual_sha" != "$kernel_expected_sha" ]]; then
  echo "kernel checksum mismatch: expected=$kernel_expected_sha actual=$kernel_actual_sha" >&2
  exit 1
fi
if [[ "$rootfs_actual_sha" != "$rootfs_expected_sha" ]]; then
  echo "rootfs checksum mismatch: expected=$rootfs_expected_sha actual=$rootfs_actual_sha" >&2
  exit 1
fi
if [[ "$firecracker_actual_sha" != "$firecracker_expected_sha" ]]; then
  echo "firecracker tgz checksum mismatch: expected=$firecracker_expected_sha actual=$firecracker_actual_sha" >&2
  exit 1
fi

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
  --arg kernel_src_sha "$kernel_actual_sha" \
  --arg rootfs_src_sha "$rootfs_actual_sha" \
  --arg firecracker_src_sha "$firecracker_actual_sha" \
  --arg kernel_sha "$(sha256sum "$out_dir/vmlinux" | awk '{print $1}')" \
  --arg rootfs_sha "$(sha256sum "$out_dir/rootfs.ext4" | awk '{print $1}')" \
  --arg firecracker_sha "$(sha256sum "$out_dir/firecracker" | awk '{print $1}')" \
  '{image_sha:$image_sha,sources:{kernel:$kernel_url,rootfs:$rootfs_url,firecracker:$firecracker_url},source_sha256:{kernel:$kernel_src_sha,rootfs_squashfs:$rootfs_src_sha,firecracker_tgz:$firecracker_src_sha},sha256:{kernel:$kernel_sha,rootfs:$rootfs_sha,firecracker:$firecracker_sha}}' \
  > "$out_dir/metadata.json"

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$out_dir/.complete"
echo "image built: $out_dir"
