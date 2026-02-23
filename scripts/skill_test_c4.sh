#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

go test ./internal/skill/refine -count=1
go test ./internal/store -count=1
go test ./cmd/virmux -count=1 -run 'Test(CmdSkillRefine|ExportImportDeterministicRoundTrip)'

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/skill-test-c4.ok"
echo "skill:test:c4: OK"
