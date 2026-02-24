#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

go test ./cmd/virmux -count=1 -run 'TestCanary(Snapshot|Run)Script'
go test ./internal/store -count=1 -run 'TestStoreSchemaAndFK'
./scripts/dset_lint.sh

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/skill-test-c5.ok"
echo "skill:test:c5: OK"
