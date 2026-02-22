#!/usr/bin/env bash
set -euo pipefail

go test ./internal/skill/... ./cmd/virmux -run 'Test(LoadDir|LintDirs|BudgetTracker|VerifyReplayHashes|ParseReadyBanner)' -count=1
go run ./cmd/virmux skill lint skills/dd >/dev/null

mkdir -p tmp
date -u +%FT%TZ > tmp/skill-test-core.ok
