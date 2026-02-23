#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

go test ./internal/skill/eval -count=1
go test ./cmd/virmux -count=1 -run 'TestCmdSkillPromote'
./scripts/skill_eval.sh
./scripts/skill_ab.sh
./scripts/skill_sql_cert.sh

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/skill-test-c3.ok"
echo "skill:test:c3: OK"
