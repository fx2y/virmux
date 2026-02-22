#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"
root="$(repo_root)"
cd "$root"

lock_sha="$(read_image_sha_lock)"
src_dir="$root/.cache/ghostfleet/images/$lock_sha"
if [[ ! -f "$src_dir/firecracker" || ! -f "$src_dir/vmlinux" || ! -f "$src_dir/rootfs.ext4" ]]; then
  echo "image:seed test needs existing lock-selected artifacts at $src_dir" >&2
  exit 1
fi

tmp_src="$(mktemp -d "$root/tmp/image-seed-src.XXXXXX")"
tmp_dst="$(mktemp -d "$root/tmp/image-seed-dst.XXXXXX")"
cleanup() {
  rm -rf "$tmp_src" "$tmp_dst"
}
trap cleanup EXIT

install -m 0755 "$src_dir/firecracker" "$tmp_src/firecracker"
install -m 0644 "$src_dir/vmlinux" "$tmp_src/vmlinux"
install -m 0644 "$src_dir/rootfs.ext4" "$tmp_src/rootfs.ext4"
if [[ -f "$src_dir/metadata.json" ]]; then
  install -m 0644 "$src_dir/metadata.json" "$tmp_src/metadata.json"
fi

VIRMUX_IMAGE_SEED_DEST_DIR="$tmp_dst" ./scripts/image_seed.sh "$tmp_src" >/dev/null

for f in firecracker vmlinux rootfs.ext4 .complete seeded.json; do
  if [[ ! -f "$tmp_dst/$f" ]]; then
    echo "image:seed test missing output $tmp_dst/$f" >&2
    exit 1
  fi
done

if ! cmp -s "$tmp_src/vmlinux" "$tmp_dst/vmlinux"; then
  echo "image:seed test vmlinux mismatch" >&2
  exit 1
fi

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$root/tmp/image-seed.ok"
echo "image seed: OK"
