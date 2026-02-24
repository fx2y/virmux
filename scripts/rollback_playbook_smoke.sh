#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

go test ./internal/skill/promosvc -run TestServiceRunRollback -count=1
go test ./cmd/virmux -run TestCmdSkillPromoteRollbackWritesAuditRow -count=1

mkdir -p "$root/tmp"
date -u +%FT%TZ > "$root/tmp/rollback-playbook-smoke.ok"
echo "rollback:playbook:smoke: OK"
