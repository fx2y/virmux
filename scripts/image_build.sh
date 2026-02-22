#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

root="$(repo_root)"
out_dir="$(image_dir)"
agentd_host_bin="$root/tmp/virmux-agentd-linux-amd64"
mkdir -p "$(dirname "$out_dir")"
mkdir -p "$root/tmp"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$agentd_host_bin" ./cmd/virmux-agentd

if [[ -f "$out_dir/.complete" ]]; then
  echo "image cache hit: $out_dir"
  exit 0
fi

if command -v docker >/dev/null 2>&1; then
  host_uid="$(id -u)"
  host_gid="$(id -g)"
  docker run --rm \
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
else
  VIRMUX_AGENTD_HOST_BIN="$agentd_host_bin" ./scripts/image_build_inner.sh
fi

sha="$(calc_image_sha)"
mkdir -p "$root/.cache/ghostfleet/images"
printf '%s\n' "$sha" > "$root/.cache/ghostfleet/images/.manifest-built"
