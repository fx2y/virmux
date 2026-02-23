#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

go test ./internal/skill/motif -count=1
go test ./cmd/virmux -count=1 -run 'TestCmdSkillSuggest'

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/skill-test-c5.ok"
echo "skill:test:c5: OK"
